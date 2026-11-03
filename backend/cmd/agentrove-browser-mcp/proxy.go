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

const (
	defaultCallTimeout = 90 * time.Second
	// The proxy's own calls (claiming a tab) are the first of a new
	// playwright-mcp, so they include connecting to the browser (at most
	// --cdp-timeout).
	defaultInternalTimeout = 40 * time.Second
	// A call the client cancelled is given this long to finish before
	// playwright-mcp is restarted under it: it may be what hangs.
	cancelGrace = 5 * time.Second
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
	// Stop ends it gracefully; Kill at once, dropping its CDP connection
	// (and so every session and emulation it set up).
	Stop()
	Kill()
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
	// callTimeout bounds every tools/call (0: none). Neither playwright-mcp
	// nor the CLI bound them: a hung page, input dispatch or run_code
	// would hold the agent forever. On expiry the agent gets a tool error
	// and playwright-mcp is killed, so whatever hung goes with its
	// connection, and the next call starts over.
	callTimeout     time.Duration
	internalTimeout time.Duration
	// heal runs before each playwright-mcp start: it closes hung pages and
	// stops a hung browser (see browserCtl.heal), which would otherwise make
	// it time out connecting.
	heal func()
	// restartBrowser stops the browser when a new playwright-mcp couldn't
	// connect to it even so; start then launches a fresh one.
	restartBrowser func()
	// With listPages set, each client drives a tab of its own (see tabs.go).
	listPages func() ([]string, error)
	closePage func(id string)

	outMu sync.Mutex
	out   io.Writer

	mu         sync.Mutex
	be         backend
	initParams json.RawMessage
	pending    map[string]*call         // client requests the backend hasn't answered, by idKey
	internal   map[string]*internalWait // the proxy's own requests, by idKey
	seq        int
	lastUse    time.Time
	tabState
}

// call is a client request forwarded to the backend.
type call struct {
	id        json.RawMessage
	tool      string // tools/call only
	tabAction string // browser_tabs only
	timer     *time.Timer
	cancelled bool
}

func newProxy(c *cache, out io.Writer, start func() (backend, error)) *proxy {
	return &proxy{cache: c, out: out, start: start, pending: map[string]*call{}, internal: map[string]*internalWait{},
		internalTimeout: defaultInternalTimeout, tabState: tabState{owned: map[string]bool{}}}
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
	defer p.shutdown()
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
	if m.Method == "notifications/cancelled" {
		p.cancel(m.Params)
	}
	if !running && !m.isRequest() {
		// Notifications and stray responses need no backend: a new one asks
		// for roots itself.
		return
	}

	if m.Method == "tools/call" && running && p.browserUp != nil && !p.browserUp() {
		// The browser was reset or crashed; a fresh playwright-mcp reconnects.
		p.stopBackend(false)
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
	} else if m.Method == "tools/call" {
		if err := p.keepTab(); err != nil {
			// playwright-mcp or the browser couldn't even say which tab it
			// is on: start over.
			log.Printf("check the tab: %v; restarting playwright-mcp", err)
			p.stopBackend(true)
			if _, err := p.spawn(); err != nil {
				log.Print(err)
				p.failRequest(m, err)
				return
			}
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
		c := &call{id: m.ID}
		if m.Method == "tools/call" {
			var params struct {
				Name      string `json:"name"`
				Arguments struct {
					Action string `json:"action"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(m.Params, &params)
			c.tool = params.Name
			if c.tool == "browser_tabs" {
				c.tabAction = params.Arguments.Action
			}
			if p.callTimeout > 0 {
				key := idKey(m.ID)
				c.timer = time.AfterFunc(p.callTimeout, func() { p.expire(be, key) })
			}
		}
		p.pending[idKey(m.ID)] = c
	}
	p.lastUse = time.Now()
	p.mu.Unlock()
	if err := be.Send(line); err != nil {
		log.Printf("send to playwright-mcp: %v", err)
	}
}

// expire fails a tool call that ran past callTimeout and kills its
// playwright-mcp. A hung page or CDP session would keep the next call stuck
// too; the next call starts a new one, after heal has closed what is hung.
func (p *proxy) expire(be backend, key string) {
	p.mu.Lock()
	c := p.pending[key]
	if c == nil || p.be != be {
		p.mu.Unlock()
		return
	}
	delete(p.pending, key)
	p.mu.Unlock()
	if c.cancelled {
		log.Printf("cancelled %s still running; restarting playwright-mcp", c.tool)
	} else {
		log.Printf("%s didn't answer in %s; restarting playwright-mcp", c.tool, p.callTimeout)
	}
	if !c.cancelled {
		p.reply(c.id, toolError(timeoutMessage(p.callTimeout)))
	}
	p.stopIf(be, true)
}

func timeoutMessage(d time.Duration) string {
	return fmt.Sprintf("The browser call timed out after %s; the page may be stuck (a JS dialog, a busy script, "+
		"or input that never completes). The browser connection was restarted, which also undid emulation set up "+
		"through CDP sessions, and the next call reconnects. Retry, or call browser_navigate to reload the page.", d)
}

func toolError(text string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": true}
}

// cancel handles the client's notifications/cancelled: the call's answer is
// no longer wanted, and if it doesn't come soon, what it is stuck on is
// restarted rather than left to the next call.
func (p *proxy) cancel(params json.RawMessage) {
	var n struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &n) != nil || len(n.RequestID) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.pending[idKey(n.RequestID)]; c != nil && !c.cancelled {
		c.cancelled = true
		if c.timer != nil {
			c.timer.Reset(min(cancelGrace, p.callTimeout))
		}
	}
}

// spawn starts a backend, initialized with the client's initialize params,
// and puts it on the client's tab, returning its initialize result. When it
// can't connect to the browser, the browser is restarted once.
func (p *proxy) spawn() (json.RawMessage, error) {
	init, err := p.startBackend()
	if err != nil {
		return nil, err
	}
	err = p.claimTab(true)
	if err != nil && connectFailed(err) && p.restartBrowser != nil {
		log.Printf("playwright-mcp couldn't connect to the browser (%v); restarting the browser", err)
		p.stopBackend(true)
		p.restartBrowser()
		if init, err = p.startBackend(); err != nil {
			return nil, err
		}
		err = p.claimTab(true)
	}
	switch {
	case err == nil:
	case connectFailed(err):
		p.stopBackend(true)
		return nil, fmt.Errorf("connect to the browser: %w", err)
	default:
		// Still usable, just on whatever tab playwright-mcp picked.
		log.Printf("couldn't give this session its own tab: %v", err)
	}
	return init, nil
}

// connectFailed reports whether err (from the first calls of a new
// playwright-mcp) means it couldn't connect to the browser.
func connectFailed(err error) bool {
	if errors.Is(err, errInternalTimeout) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "connectOverCDP") || strings.Contains(s, "initializeServer") || strings.Contains(s, "ECONNREFUSED")
}

// startBackend heals the browser, then starts a backend and initializes it
// with the client's initialize params.
func (p *proxy) startBackend() (json.RawMessage, error) {
	if p.heal != nil {
		p.heal()
	}
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
		be.Kill()
		return nil, fmt.Errorf("initialize playwright-mcp: %w", err)
	}
	init, err := awaitResponse(be.Lines(), strings.Trim(replayID, `"`), 60*time.Second, p.writeLine)
	if err != nil {
		be.Kill()
		return nil, fmt.Errorf("initialize playwright-mcp: %w", err)
	}
	if err := be.Send([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		be.Kill()
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
		var c *call
		if len(m.ID) > 0 && m.Method == "" {
			key := idKey(m.ID)
			if w := p.internal[key]; w != nil {
				delete(p.internal, key)
				p.mu.Unlock()
				w.ch <- line
				continue
			}
			// A response for a call the client no longer waits on (failed
			// when its backend was replaced, timed out, or cancelled) is
			// dropped.
			if c = p.pending[key]; c != nil && current {
				delete(p.pending, key)
				if c.timer != nil {
					c.timer.Stop()
				}
				p.lastUse = time.Now()
			}
			current = current && c != nil && !c.cancelled
		}
		p.mu.Unlock()
		if !current {
			continue
		}
		if c != nil {
			line = p.afterCall(be, c, line)
		}
		p.writeLine(line)
	}
	p.mu.Lock()
	var orphaned []json.RawMessage
	if p.be == be {
		p.be = nil
		orphaned = p.takePending()
	}
	for key, w := range p.internal {
		if w.be == be {
			delete(p.internal, key)
			close(w.ch)
		}
	}
	p.mu.Unlock()
	// Answer what it left behind, or the CLI waits on those calls forever.
	for _, id := range orphaned {
		p.replyError(id, -32603, "The browser MCP server exited; retry the call.")
	}
}

// takePending empties pending, returning the ids still owed an answer.
// Callers hold mu.
func (p *proxy) takePending() []json.RawMessage {
	var ids []json.RawMessage
	for _, c := range p.pending {
		if c.timer != nil {
			c.timer.Stop()
		}
		if !c.cancelled {
			ids = append(ids, c.id)
		}
	}
	p.pending = map[string]*call{}
	return ids
}

// stopBackend stops the running backend, if any; kill skips the graceful
// shutdown, which a hung page could hold up.
func (p *proxy) stopBackend(kill bool) { p.stopIf(nil, kill) }

// stopIf stops the running backend if it is want (any when nil).
func (p *proxy) stopIf(want backend, kill bool) {
	p.mu.Lock()
	be := p.be
	if be == nil || (want != nil && be != want) {
		p.mu.Unlock()
		return
	}
	p.be = nil
	pending := p.takePending()
	p.mu.Unlock()
	if kill {
		be.Kill()
	} else {
		be.Stop()
	}
	for _, id := range pending {
		p.replyError(id, -32603, "The browser restarted; retry the call.")
	}
	if p.prune != nil {
		go p.prune()
	}
}

// shutdown ends the session: playwright-mcp, and the tabs it opened.
func (p *proxy) shutdown() {
	p.stopBackend(false)
	p.closeOwnedTabs()
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
		p.stopBackend(false)
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
