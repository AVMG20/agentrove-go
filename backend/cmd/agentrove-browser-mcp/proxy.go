package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"
)

// filesDir is where playwright-mcp may read and write files: its output dir,
// and the only root the proxy gives it. Tool calls that take a filename
// (screenshots, snapshots, evaluate results, response bodies, uploads)
// can't reach the project, so a prompt-injected agent can't use pre-allowed
// browser tools to write e.g. .claude/settings.json.
const filesDir = "/tmp/playwright-mcp"

// rootsResult answers playwright-mcp's roots/list in place of the client's
// roots (the project).
var rootsResult = json.RawMessage(`{"roots":[{"uri":"file://` + filesDir + `","name":"Browser files"}]}`)

// replayID is the id of the initialize the proxy sends a new playwright-mcp
// on the client's behalf; its response is swallowed.
const replayID = `"agentrove-browser-mcp-init"`

type cache struct {
	Initialize json.RawMessage `json:"initialize"` // initialize result
	Tools      json.RawMessage `json:"tools"`      // tools/list result
}

// backend is a running playwright-mcp speaking newline-delimited JSON-RPC.
type backend interface {
	Send(line []byte) error
	// Lines yields its output, one message per line; closed when it exits.
	Lines() <-chan []byte
	Stop()
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

func (m message) isRequest() bool { return len(m.ID) > 0 && m.Method != "" }

type proxy struct {
	cache     *cache
	start     func() (backend, error)
	browserUp func() bool
	idle      time.Duration
	// prune trims filesDir; run when a backend stops.
	prune func()

	outMu sync.Mutex
	out   io.Writer

	mu         sync.Mutex
	be         backend
	initParams json.RawMessage
	pending    map[string]json.RawMessage // client requests the backend hasn't answered, by idKey
	lastUse    time.Time
}

func newProxy(c *cache, out io.Writer, start func() (backend, error)) *proxy {
	return &proxy{cache: c, out: out, start: start, pending: map[string]json.RawMessage{}}
}

// idKey normalizes a JSON-RPC id so a response matches its request however
// the backend re-encodes it.
func idKey(id json.RawMessage) string {
	var v any
	if json.Unmarshal(id, &v) != nil {
		return string(id)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// run serves client messages from in until EOF or ctx ends.
func (p *proxy) run(ctx context.Context, in io.Reader) {
	if p.idle > 0 {
		go p.reapLoop(ctx)
	}
	lines := make(chan []byte)
	go func() {
		defer close(lines)
		r := bufio.NewReaderSize(in, 64<<10)
		for {
			line, err := r.ReadBytes('\n')
			if len(strings.TrimSpace(string(line))) > 0 {
				lines <- line
			}
			if err != nil {
				return
			}
		}
	}()
	defer p.stopBackend()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				return
			}
			p.handle(line)
		}
	}
}

func (p *proxy) write(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	p.writeLine(append(b, '\n'))
}

func (p *proxy) writeLine(line []byte) {
	p.outMu.Lock()
	defer p.outMu.Unlock()
	_, _ = p.out.Write(line)
}

func (p *proxy) reply(id json.RawMessage, result any) {
	p.write(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (p *proxy) replyError(id json.RawMessage, code int, msg string) {
	p.write(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": msg}})
}

func (p *proxy) handle(line []byte) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		return
	}
	if m.Method == "notifications/initialized" || m.Method == "notifications/roots/list_changed" {
		// Every backend gets its own initialized from spawn, and its roots
		// from the proxy, never the client's.
		return
	}
	p.mu.Lock()
	running := p.be != nil
	if m.Method == "initialize" {
		p.initParams = m.Params
	}
	p.mu.Unlock()

	if !running && p.cache != nil {
		switch m.Method {
		case "initialize":
			p.reply(m.ID, initResult(p.cache.Initialize, m.Params))
			return
		case "tools/list":
			p.reply(m.ID, p.cache.Tools)
			return
		case "ping":
			p.reply(m.ID, map[string]any{})
			return
		}
		if m.isRequest() && !p.needsBackend(m.Method) {
			// What playwright-mcp answers too (e.g. the CLI's server/discover
			// probe), without starting it for that.
			p.replyError(m.ID, -32601, "Method not found")
			return
		}
	}
	if !running && !m.isRequest() {
		// Notifications and stray responses need no backend: a new one asks
		// for roots itself.
		return
	}

	if m.Method == "tools/call" && running && p.browserUp != nil && !p.browserUp() {
		// The browser was reset or crashed; a fresh playwright-mcp reconnects.
		p.stopBackend()
		running = false
	}
	if !running {
		init, err := p.spawn()
		if err != nil {
			log.Print(err)
			p.failRequest(m, err)
			return
		}
		if m.Method == "initialize" {
			// No cache: the backend's own answer.
			p.reply(m.ID, json.RawMessage(init))
			return
		}
	}
	p.forward(m, line)
}

// needsBackend reports whether playwright-mcp can serve method: tool calls,
// and whatever else its cached capabilities announce.
func (p *proxy) needsBackend(method string) bool {
	var res struct {
		Capabilities map[string]json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(p.cache.Initialize, &res)
	has := func(c string) bool { _, ok := res.Capabilities[c]; return ok }
	switch {
	case method == "tools/call":
		return true
	case strings.HasPrefix(method, "resources/"):
		return has("resources")
	case strings.HasPrefix(method, "prompts/"):
		return has("prompts")
	case method == "completion/complete":
		return has("completions")
	case method == "logging/setLevel":
		return has("logging")
	}
	return false
}

// failRequest answers a request the backend couldn't be started for. Tool
// calls get a tool error, so the agent sees why instead of a protocol error.
func (p *proxy) failRequest(m message, err error) {
	if !m.isRequest() {
		return
	}
	if m.Method == "tools/call" {
		p.reply(m.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "The browser could not be started: " + err.Error()}},
			"isError": true,
		})
		return
	}
	p.replyError(m.ID, -32603, "The browser could not be started: "+err.Error())
}

func (p *proxy) forward(m message, line []byte) {
	p.mu.Lock()
	be := p.be
	if be == nil {
		p.mu.Unlock()
		p.failRequest(m, errors.New("playwright-mcp exited"))
		return
	}
	if m.isRequest() {
		p.pending[idKey(m.ID)] = m.ID
	}
	p.lastUse = time.Now()
	p.mu.Unlock()
	if err := be.Send(line); err != nil {
		log.Printf("send to playwright-mcp: %v", err)
	}
}

// spawn starts a backend and initializes it with the client's initialize
// params, returning its initialize result.
func (p *proxy) spawn() (json.RawMessage, error) {
	be, err := p.start()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	params := p.initParams
	p.mu.Unlock()
	if len(params) == 0 {
		params = json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"agentrove-browser-mcp","version":"1"}}`)
	}
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(replayID), "method": "initialize", "params": params})
	if err := be.Send(append(req, '\n')); err != nil {
		be.Stop()
		return nil, fmt.Errorf("initialize playwright-mcp: %w", err)
	}
	init, err := awaitResponse(be.Lines(), strings.Trim(replayID, `"`), 60*time.Second, p.writeLine)
	if err != nil {
		be.Stop()
		return nil, fmt.Errorf("initialize playwright-mcp: %w", err)
	}
	if err := be.Send([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		be.Stop()
		return nil, err
	}
	p.mu.Lock()
	p.be = be
	p.lastUse = time.Now()
	p.mu.Unlock()
	go p.pump(be)
	return init, nil
}

// awaitResponse reads lines until the response with the given string id,
// passing anything else to other (if set).
func awaitResponse(lines <-chan []byte, id string, timeout time.Duration, other func([]byte)) (json.RawMessage, error) {
	want, _ := json.Marshal(id)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil, errors.New("timed out")
		case line, ok := <-lines:
			if !ok {
				return nil, errors.New("exited before answering")
			}
			var r struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
			}
			if json.Unmarshal(line, &r) == nil && r.Method == "" && string(r.ID) == string(want) {
				if len(r.Error) > 0 && string(r.Error) != "null" {
					return nil, fmt.Errorf("error %s", r.Error)
				}
				return r.Result, nil
			}
			if other != nil {
				other(line)
			}
		}
	}
}

// pump copies backend output to the client until the backend exits.
func (p *proxy) pump(be backend) {
	for line := range be.Lines() {
		var m message
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.isRequest() && m.Method == "roots/list" {
			resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": rootsResult})
			_ = be.Send(append(resp, '\n'))
			continue
		}
		p.mu.Lock()
		current := p.be == be
		if current && len(m.ID) > 0 && m.Method == "" {
			// A response for a call the client no longer waits on (already
			// failed when its backend was replaced) is dropped.
			key := idKey(m.ID)
			_, current = p.pending[key]
			delete(p.pending, key)
			p.lastUse = time.Now()
		}
		p.mu.Unlock()
		if current {
			p.writeLine(line)
		}
	}
	p.mu.Lock()
	var orphaned []json.RawMessage
	if p.be == be {
		p.be = nil
		for _, id := range p.pending {
			orphaned = append(orphaned, id)
		}
		p.pending = map[string]json.RawMessage{}
	}
	p.mu.Unlock()
	// Answer what it left behind, or the CLI waits on those calls forever.
	for _, id := range orphaned {
		p.replyError(id, -32603, "The browser MCP server exited; retry the call.")
	}
}

func (p *proxy) stopBackend() {
	p.mu.Lock()
	be := p.be
	p.be = nil
	pending := p.pending
	p.pending = map[string]json.RawMessage{}
	p.mu.Unlock()
	if be == nil {
		return
	}
	be.Stop()
	for _, id := range pending {
		p.replyError(id, -32603, "The browser restarted; retry the call.")
	}
	if p.prune != nil {
		go p.prune()
	}
}

func (p *proxy) reapLoop(ctx context.Context) {
	tick := min(p.idle/4, 30*time.Second)
	if tick <= 0 {
		tick = time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.reapIdle()
		}
	}
}

func (p *proxy) reapIdle() {
	p.mu.Lock()
	idle := p.be != nil && len(p.pending) == 0 && time.Since(p.lastUse) >= p.idle
	p.mu.Unlock()
	if idle {
		p.stopBackend()
	}
}

// initResult is the cached initialize result with the protocol version
// negotiated for this client: its own when the cached server's is newer
// (MCP servers keep supporting older versions), else the server's.
func initResult(cached, params json.RawMessage) json.RawMessage {
	var res map[string]any
	if json.Unmarshal(cached, &res) != nil {
		return cached
	}
	var req struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(params, &req)
	server, _ := res["protocolVersion"].(string)
	// Versions are dates (YYYY-MM-DD), so they compare as strings.
	if req.ProtocolVersion != "" && (server == "" || req.ProtocolVersion < server) {
		res["protocolVersion"] = req.ProtocolVersion
	}
	b, _ := json.Marshal(res)
	return b
}
