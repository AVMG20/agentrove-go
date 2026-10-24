package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBackend answers initialize and tools/call like playwright-mcp.
type fakeBackend struct {
	mu      sync.Mutex
	got     []message
	raw     [][]byte
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
	f.raw = append(f.raw, line)
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

// playwright-mcp asks for roots to decide where tool calls may read and
// write files; it must get filesDir, never the client's (the project).
func TestRootsAreConfinedToFilesDir(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	out.response(t, "1")
	f := (*started)[0]
	f.lines <- []byte(`{"jsonrpc":"2.0","id":"r1","method":"roots/list"}` + "\n")
	waitFor := func(cond func() bool) {
		deadline := time.Now().Add(2 * time.Second)
		for !cond() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	var answer []byte
	waitFor(func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range f.raw {
			if strings.Contains(string(l), `"r1"`) {
				answer = l
				return true
			}
		}
		return false
	})
	var resp struct {
		Result struct {
			Roots []struct{ URI string } `json:"roots"`
		} `json:"result"`
	}
	if json.Unmarshal(answer, &resp) != nil || len(resp.Result.Roots) != 1 || resp.Result.Roots[0].URI != "file://"+filesDir {
		t.Fatalf("roots answer = %s", answer)
	}
	// The client never sees the request, and its root changes aren't passed on.
	p.handle([]byte(`{"jsonrpc":"2.0","method":"notifications/roots/list_changed"}`))
	out.mu.Lock()
	text := out.b.String()
	out.mu.Unlock()
	if strings.Contains(text, "roots/list") {
		t.Fatalf("roots request reached the client: %s", text)
	}
	if strings.Contains(strings.Join(f.methods(), ","), "roots/list_changed") {
		t.Fatal("client roots change reached playwright-mcp")
	}
}

// A late answer from a replaced backend, for a call already failed, must
// not reach the client a second time.
func TestLateResponseFromOldBackendIsDropped(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	up := true
	p.browserUp = func() bool { return up }
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`))
	out.response(t, "1")
	old := (*started)[0]
	old.mu.Lock()
	old.silent = true
	old.mu.Unlock()
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{}}`))
	up = false
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{}}`))
	if out.response(t, "2")["error"] == nil {
		t.Fatal("call 2 not failed on restart")
	}
	out.response(t, "3")
	// The old process answers call 2 on its way out.
	old.mu.Lock()
	old.stopped = false
	old.lines = make(chan []byte, 4)
	old.mu.Unlock()
	go p.pump(old)
	old.lines <- []byte(`{"jsonrpc":"2.0","id":2,"result":{"content":[]}}` + "\n")
	close(old.lines)
	time.Sleep(50 * time.Millisecond)
	out.mu.Lock()
	n := strings.Count(out.b.String(), `"id":2`)
	out.mu.Unlock()
	if n != 1 {
		t.Fatalf("client got %d answers for call 2", n)
	}
}

func TestPruneDir(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	for i := range 10 {
		path := filepath.Join(dir, fmt.Sprintf("f%d.yml", i))
		if err := os.WriteFile(path, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, now, now.Add(time.Duration(i)*time.Second))
	}
	pruneDir(dir, 4, 1<<20)
	left, _ := os.ReadDir(dir)
	if len(left) != 4 || left[0].Name() != "f6.yml" {
		t.Fatalf("left %v", left)
	}
	pruneDir(dir, 100, 250)
	if left, _ = os.ReadDir(dir); len(left) != 2 {
		t.Fatalf("left %v", left)
	}
}

// A file moved in with an old mtime is new by its ctime and survives.
func TestPruneDirKeepsMovedInFiles(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for i := range 3 {
		path := filepath.Join(dir, fmt.Sprintf("f%d", i))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(path, old, old.Add(time.Duration(i)*time.Minute))
	}
	time.Sleep(20 * time.Millisecond)
	moved := filepath.Join(dir, "moved")
	src := filepath.Join(t.TempDir(), "moved")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(src, old, old.Add(-24*time.Hour)) // oldest mtime of all
	if err := os.Rename(src, moved); err != nil {
		t.Fatal(err)
	}
	pruneDir(dir, 1, 1<<20)
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("moved-in file pruned: %v", err)
	}
}
