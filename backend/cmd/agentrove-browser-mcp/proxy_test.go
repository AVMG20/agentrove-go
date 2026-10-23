package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend answers initialize and tools/call like playwright-mcp.
type fakeBackend struct {
	mu      sync.Mutex
	got     []message
	lines   chan []byte
	stopped bool
	// silent drops tools/call requests (a call still running).
	silent bool
}

func newFake() *fakeBackend { return &fakeBackend{lines: make(chan []byte, 64)} }

func (f *fakeBackend) Send(line []byte) error {
	var m message
	_ = json.Unmarshal(line, &m)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopped {
		return errors.New("stopped")
	}
	f.got = append(f.got, m)
	switch m.Method {
	case "initialize":
		f.lines <- []byte(`{"jsonrpc":"2.0","method":"notifications/message","params":{}}` + "\n")
		f.lines <- []byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"Playwright"}}}` + "\n")
	case "tools/call":
		if !f.silent {
			f.lines <- []byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"content":[{"type":"text","text":"ok"}]}}` + "\n")
		}
	}
	return nil
}

func (f *fakeBackend) Lines() <-chan []byte { return f.lines }

func (f *fakeBackend) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.stopped {
		f.stopped = true
		close(f.lines)
	}
}

func (f *fakeBackend) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.got {
		out = append(out, m.Method)
	}
	return out
}

// syncBuffer collects proxy output for assertions.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// response waits for the response with id and returns it.
func (s *syncBuffer) response(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		text := s.b.String()
		s.mu.Unlock()
		for _, line := range strings.Split(text, "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) == nil {
				if raw, _ := json.Marshal(m["id"]); string(raw) == id && m["method"] == nil {
					return m
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no response with id %s in %q", id, s.b.String())
	return nil
}

var testCache = &cache{
	Initialize: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"Playwright"}}`),
	Tools:      json.RawMessage(`{"tools":[{"name":"browser_navigate"}]}`),
}

const clientInit = `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{"roots":{}},"clientInfo":{"name":"claude-code"}}}`

func newTestProxy(c *cache) (*proxy, *syncBuffer, *[]*fakeBackend) {
	out := &syncBuffer{}
	var started []*fakeBackend
	var mu sync.Mutex
	p := newProxy(c, out, func() (backend, error) {
		mu.Lock()
		defer mu.Unlock()
		f := newFake()
		started = append(started, f)
		return f, nil
	})
	return p, out, &started
}

func TestCachedHandshakeStartsNothing(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))

	init := out.response(t, "0")["result"].(map[string]any)
	// The client asked for an older version than the cached server's.
	if init["protocolVersion"] != "2025-03-26" {
		t.Fatalf("protocolVersion = %v", init["protocolVersion"])
	}
	tools := out.response(t, "1")["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	out.response(t, "2")
	// Claude Code probes for newer protocol revisions first; playwright-mcp
	// has no resources either.
	p.handle([]byte(`{"jsonrpc":"2.0","id":"server-discover-probe-1","method":"server/discover","params":{}}`))
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"resources/list"}`))
	if out.response(t, `"server-discover-probe-1"`)["error"] == nil || out.response(t, "3")["error"] == nil {
		t.Fatal("unsupported methods not refused")
	}
	if len(*started) != 0 {
		t.Fatalf("started %d backends before any tool call", len(*started))
	}
}

func TestToolCallStartsAndReplaysInitialize(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	p.handle([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	res := out.response(t, "7")
	if res["result"].(map[string]any)["content"] == nil {
		t.Fatalf("unexpected response %v", res)
	}
	if len(*started) != 1 {
		t.Fatalf("started %d backends", len(*started))
	}
	f := (*started)[0]
	if got := strings.Join(f.methods(), ","); got != "initialize,notifications/initialized,tools/call" {
		t.Fatalf("backend got %s", got)
	}
	// The replayed initialize carries the client's own params.
	var params map[string]any
	_ = json.Unmarshal(f.got[0].Params, &params)
	if params["clientInfo"].(map[string]any)["name"] != "claude-code" {
		t.Fatalf("replayed params = %v", params)
	}
	// The replay's answer is swallowed; other output passes through.
	out.mu.Lock()
	text := out.b.String()
	out.mu.Unlock()
	if strings.Contains(text, "agentrove-browser-mcp-init") || !strings.Contains(text, "notifications/message") {
		t.Fatalf("output = %s", text)
	}

	// The next call reuses it.
	p.handle([]byte(`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{}}`))
	out.response(t, "8")
	if len(*started) != 1 {
		t.Fatalf("started %d backends", len(*started))
	}
}

func TestIdleBackendIsStoppedAndRestarted(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.idle = time.Minute
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	out.response(t, "1")

	p.reapIdle()
	if (*started)[0].stopped {
		t.Fatal("stopped a backend used just now")
	}
	p.mu.Lock()
	p.lastUse = time.Now().Add(-2 * time.Minute)
	p.mu.Unlock()
	p.reapIdle()
	if !(*started)[0].stopped {
		t.Fatal("idle backend still running")
	}
	// Answered from the cache again.
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	out.response(t, "2")
	if len(*started) != 1 {
		t.Fatalf("started %d backends", len(*started))
	}
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`))
	out.response(t, "3")
	if len(*started) != 2 {
		t.Fatalf("started %d backends", len(*started))
	}
}

func TestBusyBackendIsNotReaped(t *testing.T) {
	p, _, started := newTestProxy(testCache)
	p.idle = time.Minute
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	f := (*started)[0]
	f.mu.Lock()
	f.silent = true
	f.mu.Unlock()
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`))
	p.mu.Lock()
	p.lastUse = time.Now().Add(-2 * time.Minute)
	p.mu.Unlock()
	p.reapIdle()
	if f.stopped {
		t.Fatal("reaped a backend with a call in flight")
	}
}

func TestBackendExitFailsPendingCalls(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	out.response(t, "1")
	f := (*started)[0]
	f.mu.Lock()
	f.silent = true
	f.mu.Unlock()
	p.handle([]byte(`{"jsonrpc":"2.0","id":"x","method":"tools/call","params":{}}`))
	f.Stop()
	if out.response(t, `"x"`)["error"] == nil {
		t.Fatal("pending call not failed")
	}
}

func TestStartFailureIsAToolError(t *testing.T) {
	out := &syncBuffer{}
	p := newProxy(testCache, out, func() (backend, error) { return nil, errors.New("no chromium") })
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{}}`))
	res := out.response(t, "5")["result"].(map[string]any)
	if res["isError"] != true || !strings.Contains(res["content"].([]any)[0].(map[string]any)["text"].(string), "no chromium") {
		t.Fatalf("result = %v", res)
	}
}

func TestBrowserDownRestartsBackend(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	up := true
	p.browserUp = func() bool { return up }
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	out.response(t, "1")
	up = false
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`))
	out.response(t, "2")
	if len(*started) != 2 || !(*started)[0].stopped {
		t.Fatalf("started %d backends", len(*started))
	}
}

func TestNoCacheStartsEagerly(t *testing.T) {
	p, out, started := newTestProxy(nil)
	p.handle([]byte(clientInit))
	if out.response(t, "0")["result"].(map[string]any)["serverInfo"] == nil {
		t.Fatal("no initialize result")
	}
	p.handle([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if len(*started) != 1 {
		t.Fatalf("started %d backends", len(*started))
	}
	// Only the proxy's own initialized notification reaches the backend.
	if got := strings.Join((*started)[0].methods(), ","); got != "initialize,notifications/initialized" {
		t.Fatalf("backend got %s", got)
	}
}

func TestInitResultVersion(t *testing.T) {
	cached := json.RawMessage(`{"protocolVersion":"2025-06-18"}`)
	for req, want := range map[string]string{"2024-11-05": "2024-11-05", "2025-06-18": "2025-06-18", "2026-01-01": "2025-06-18", "": "2025-06-18"} {
		var got map[string]any
		_ = json.Unmarshal(initResult(cached, json.RawMessage(`{"protocolVersion":"`+req+`"}`)), &got)
		if got["protocolVersion"] != want {
			t.Errorf("client %q: got %v, want %s", req, got["protocolVersion"], want)
		}
	}
}
