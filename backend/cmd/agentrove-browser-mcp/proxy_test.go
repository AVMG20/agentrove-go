package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	killed  bool
	// silent drops tools/call requests (a call still running).
	silent bool

	// With browser set, it serves the proxy's tab calls on its pages.
	browser *fakeBrowser
	current string // its current tab
	ranOn   []string
	// connectErr fails every call, as when connecting to the browser fails.
	connectErr string
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
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(m.Params, &params)
		if f.connectErr != "" {
			f.answer(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": "### Error\n" + f.connectErr}}, "isError": true})
			return nil
		}
		if _, internal := params.Arguments["_meta"]; internal && f.browser != nil {
			f.answer(m.ID, map[string]any{"content": []map[string]any{{"type": "text", "text": f.internal(params.Name, params.Arguments)}}})
			return nil
		}
		f.ranOn = append(f.ranOn, f.current)
		if f.browser != nil && params.Name == "browser_tabs" && params.Arguments["action"] == "new" {
			f.current = f.browser.newPage()
		}
		if !f.silent {
			f.lines <- []byte(`{"jsonrpc":"2.0","id":` + string(m.ID) + `,"result":{"content":[{"type":"text","text":"ok"}]}}` + "\n")
		}
	}
	return nil
}

func (f *fakeBackend) answer(id json.RawMessage, result any) {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	f.lines <- append(b, '\n')
}

// internal serves the proxy's own calls, in JSON mode.
func (f *fakeBackend) internal(name string, args map[string]any) string {
	result := ""
	switch name {
	case "browser_tabs":
		switch args["action"] {
		case "new":
			f.current = f.browser.newPage()
		case "select":
			pages, _ := f.browser.listPages()
			f.current = pages[int(args["index"].(float64))]
		}
	case "browser_run_code_unsafe":
		code := args["code"].(string)
		if strings.Contains(code, "pages()") {
			pages, _ := f.browser.listPages()
			i := -1
			for j, id := range pages {
				if strings.Contains(code, `"`+id+`"`) {
					i = j
				}
			}
			result = fmt.Sprint(i)
		} else {
			result = `"` + f.current + `"`
		}
	}
	b, _ := json.Marshal(map[string]any{"result": result})
	return string(b)
}

// fakeBrowser is the browser fake backends share: its page targets.
type fakeBrowser struct {
	mu     sync.Mutex
	pages  []string
	n      int
	closed []string
}

func (b *fakeBrowser) newPage() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.n++
	id := fmt.Sprintf("%08X", 0xA0000000+b.n)
	b.pages = append(b.pages, id)
	return id
}

func (b *fakeBrowser) listPages() ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.pages), nil
}

func (b *fakeBrowser) closePage(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pages = slices.DeleteFunc(b.pages, func(p string) bool { return p == id })
	b.closed = append(b.closed, id)
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

func (f *fakeBackend) Kill() {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	f.Stop()
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

// has reports whether the client got a response with id.
func (s *syncBuffer) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, line := range strings.Split(s.b.String(), "\n") {
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			if raw, _ := json.Marshal(m["id"]); string(raw) == id && m["method"] == nil {
				return true
			}
		}
	}
	return false
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func resultText(t *testing.T, res map[string]any) string {
	t.Helper()
	r, _ := res["result"].(map[string]any)
	content, _ := r["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("no content in %v", res)
	}
	return content[0].(map[string]any)["text"].(string)
}

func (f *fakeBackend) setSilent(v bool) {
	f.mu.Lock()
	f.silent = v
	f.mu.Unlock()
}

func (f *fakeBackend) isKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

func TestCallTimeoutFailsTheCallAndRestarts(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.callTimeout = 50 * time.Millisecond
	heals := 0
	p.heal = func() { heals++ }
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	old := (*started)[0]
	old.setSilent(true)
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_run_code_unsafe"}}`))
	res := out.response(t, "2")
	if r := res["result"].(map[string]any); r["isError"] != true || !strings.Contains(resultText(t, res), "timed out after 50ms") {
		t.Fatalf("response = %v", res)
	}
	waitUntil(t, "kill", old.isKilled)
	// The next call gets a new playwright-mcp, started after a heal.
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	if resultText(t, out.response(t, "3")) != "ok" {
		t.Fatal("call after the timeout failed")
	}
	if len(*started) != 2 || heals != 2 {
		t.Fatalf("started %d backends, %d heals", len(*started), heals)
	}
}

// An answer within the deadline stops its timer.
func TestAnsweredCallIsNotTimedOut(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.callTimeout = 50 * time.Millisecond
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	time.Sleep(100 * time.Millisecond)
	if (*started)[0].isKilled() || strings.Count(out.b.String(), `"id":1`) != 1 {
		t.Fatalf("answered call timed out: %s", out.b.String())
	}
}

// A call the client cancelled gets no answer, but if it stays stuck its
// playwright-mcp is restarted soon rather than at the deadline.
func TestCancelledStuckCallRestartsSoon(t *testing.T) {
	p, out, started := newTestProxy(testCache)
	p.callTimeout = time.Hour
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	f := (*started)[0]
	f.setSilent(true)
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_run_code_unsafe"}}`))
	p.mu.Lock()
	p.callTimeout = 50 * time.Millisecond // the grace is min(cancelGrace, callTimeout)
	p.mu.Unlock()
	p.handle([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2}}`))
	waitUntil(t, "kill", f.isKilled)
	if out.has("2") {
		t.Fatal("cancelled call was answered")
	}
	// The cancellation reached playwright-mcp too.
	if !slices.Contains(f.methods(), "notifications/cancelled") {
		t.Fatalf("backend got %v", f.methods())
	}
}

func newTabProxy(c *cache) (*proxy, *syncBuffer, *[]*fakeBackend, *fakeBrowser) {
	out := &syncBuffer{}
	br := &fakeBrowser{}
	br.newPage() // the user's page
	var started []*fakeBackend
	var mu sync.Mutex
	p := newProxy(c, out, func() (backend, error) {
		mu.Lock()
		defer mu.Unlock()
		f := newFake()
		f.browser = br
		// playwright-mcp starts on the browser's first page.
		if pages, _ := br.listPages(); len(pages) > 0 {
			f.current = pages[0]
		}
		started = append(started, f)
		return f, nil
	})
	p.listPages = br.listPages
	p.closePage = br.closePage
	return p, out, &started, br
}

func (f *fakeBackend) ran() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.ranOn)
}

func TestEachClientDrivesItsOwnTab(t *testing.T) {
	a, outA, startedA, br := newTabProxy(testCache)
	b, outB, startedB, _ := newTabProxy(testCache)
	b.listPages, b.closePage = br.listPages, br.closePage
	b.start = func() (backend, error) {
		f := newFake()
		f.browser = br
		pages, _ := br.listPages()
		f.current = pages[0]
		*startedB = append(*startedB, f)
		return f, nil
	}
	userPage := br.pages[0]
	for _, p := range []*proxy{a, b} {
		p.handle([]byte(clientInit))
		p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	}
	outA.response(t, "1")
	outB.response(t, "1")
	ranA, ranB := (*startedA)[0].ran(), (*startedB)[0].ran()
	if len(ranA) != 1 || len(ranB) != 1 || ranA[0] == userPage || ranB[0] == userPage || ranA[0] == ranB[0] {
		t.Fatalf("user page %s, A ran on %v, B on %v", userPage, ranA, ranB)
	}
	if a.tab != ranA[0] || !a.owned[ranA[0]] {
		t.Fatalf("A's tab %q, owned %v", a.tab, a.owned)
	}
}

// After an idle stop, the new playwright-mcp is put back on the client's
// tab instead of the browser's first page.
func TestTabIsReselectedAfterRestart(t *testing.T) {
	p, out, started, br := newTabProxy(testCache)
	p.idle = time.Minute
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	tab := p.tab
	p.mu.Lock()
	p.lastUse = time.Now().Add(-2 * time.Minute)
	p.mu.Unlock()
	p.reapIdle()
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_snapshot"}}`))
	out.response(t, "2")
	if len(*started) != 2 || len(br.pages) != 2 {
		t.Fatalf("%d backends, pages %v", len(*started), br.pages)
	}
	if ran := (*started)[1].ran(); len(ran) != 1 || ran[0] != tab {
		t.Fatalf("ran on %v, want %s", ran, tab)
	}
}

// A tab closed under the client (by the user, or because it hung) is
// replaced, so playwright-mcp doesn't move onto someone else's.
func TestClosedTabIsReplaced(t *testing.T) {
	p, out, started, br := newTabProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	old := p.tab
	br.closePage(old)
	f := (*started)[0]
	f.mu.Lock()
	f.current = br.pages[0] // where playwright-mcp moves
	f.mu.Unlock()
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	res := out.response(t, "2")
	ran := f.ran()
	if p.tab == old || ran[1] != p.tab || ran[1] == br.pages[0] {
		t.Fatalf("tab %s (was %s), ran on %v, pages %v", p.tab, old, ran, br.pages)
	}
	// The agent is told, once.
	content := res["result"].(map[string]any)["content"].([]any)
	if len(content) != 2 || content[0].(map[string]any)["text"] != lostTabNote || content[1].(map[string]any)["text"] != "ok" {
		t.Fatalf("result = %v", res)
	}
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	if resultText(t, out.response(t, "3")) != "ok" {
		t.Fatal("note repeated")
	}
}

func TestBrowserTabsMovesAreFollowed(t *testing.T) {
	p, out, started, br := newTabProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_tabs","arguments":{"action":"new"}}}`))
	out.response(t, "1")
	first := p.tab
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "2")
	second := (*started)[0].ran()[1]
	if p.tab != second || second == first || !p.owned[first] || !p.owned[second] {
		t.Fatalf("tab %s, owned %v, pages %v", p.tab, p.owned, br.pages)
	}
	// Session end closes both, not the user's page.
	user := br.pages[0]
	p.shutdown()
	if len(br.pages) != 1 || br.pages[0] != user {
		t.Fatalf("pages after shutdown %v", br.pages)
	}
}

// browser_close only disconnects playwright-mcp from a CDP browser: the
// proxy closes the client's tab and starts over on a new one.
func TestBrowserCloseClosesTheTab(t *testing.T) {
	p, out, started, br := newTabProxy(testCache)
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	tab := p.tab
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_close"}}`))
	out.response(t, "2")
	waitUntil(t, "stop", func() bool {
		(*started)[0].mu.Lock()
		defer (*started)[0].mu.Unlock()
		return (*started)[0].stopped
	})
	if slices.Contains(br.pages, tab) {
		t.Fatalf("tab %s still open: %v", tab, br.pages)
	}
	p.handle([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "3")
	if len(*started) != 2 || p.tab == "" || p.tab == tab || (*started)[1].ran()[0] != p.tab {
		t.Fatalf("%d backends, tab %s", len(*started), p.tab)
	}
}

func TestSessionEndClosesOwnedTabs(t *testing.T) {
	p, _, _, br := newTabProxy(testCache)
	user := br.pages[0]
	in := strings.NewReader(clientInit + "\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}` + "\n")
	p.run(t.Context(), in)
	if len(br.closed) != 1 || len(br.pages) != 1 || br.pages[0] != user {
		t.Fatalf("closed %v, left %v", br.closed, br.pages)
	}
}

// A playwright-mcp that can't connect (a wedged browser) gets the browser
// restarted once.
func TestConnectFailureRestartsTheBrowserOnce(t *testing.T) {
	for _, recovers := range []bool{true, false} {
		p, out, started, _ := newTabProxy(testCache)
		start := p.start
		p.start = func() (backend, error) {
			be, err := start()
			if f := be.(*fakeBackend); len(*started) == 1 || !recovers {
				f.connectErr = "TimeoutError: browserType.connectOverCDP: Timeout 25000ms exceeded."
			}
			return be, err
		}
		restarts := 0
		p.restartBrowser = func() { restarts++ }
		p.handle([]byte(clientInit))
		p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
		res := out.response(t, "1")
		if restarts != 1 || len(*started) != 2 || !(*started)[0].isKilled() {
			t.Fatalf("recovers=%v: %d restarts, %d backends", recovers, restarts, len(*started))
		}
		if text := resultText(t, res); recovers != (text == "ok") {
			t.Fatalf("recovers=%v: %s", recovers, text)
		}
	}
}

// A claim that times out counts as a failed connection too.
func TestClaimTimeoutIsAConnectFailure(t *testing.T) {
	p, out, started, _ := newTabProxy(testCache)
	p.internalTimeout = 50 * time.Millisecond
	start := p.start
	p.start = func() (backend, error) {
		be, err := start()
		if len(*started) == 1 {
			be.(*fakeBackend).browser = nil // ignores the proxy's calls
			be.(*fakeBackend).silent = true
		}
		return be, err
	}
	restarts := 0
	p.restartBrowser = func() { restarts++ }
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	if resultText(t, out.response(t, "1")) != "ok" || restarts != 1 {
		t.Fatalf("%d restarts", restarts)
	}
}

func TestToolResult(t *testing.T) {
	for line, want := range map[string]string{
		`{"id":1,"result":{"content":[{"type":"text","text":"{\"result\":\"\\\"ABC\\\"\"}"}]}}`:                                `"ABC"`,
		`{"id":1,"result":{"content":[{"type":"text","text":"{\"isError\":true,\"error\":\"Error: boom\"}"}],"isError":true}}`: "error Error: boom",
		`{"id":1,"result":{"content":[{"type":"text","text":"### Error\nTimeoutError: x"}],"isError":true}}`:                   "error ### Error\nTimeoutError: x",
		`{"id":1,"error":{"code":-32603,"message":"bad"}}`:                                                                     "error bad",
	} {
		got, err := toolResult([]byte(line))
		if err != nil {
			got = "error " + err.Error()
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", line, got, want)
		}
	}
}

// A browser that stops answering between calls gets a new playwright-mcp
// (whose heal restarts it) instead of a call that hangs until the deadline.
func TestUnresponsiveBrowserIsHealedBeforeTheCall(t *testing.T) {
	p, out, started, br := newTabProxy(testCache)
	heals := 0
	p.heal = func() { heals++ }
	p.handle([]byte(clientInit))
	p.handle([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	out.response(t, "1")
	hung := true
	p.listPages = func() ([]string, error) {
		if hung {
			hung = false
			return nil, errors.New("context deadline exceeded")
		}
		return br.listPages()
	}
	p.handle([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"browser_navigate"}}`))
	if resultText(t, out.response(t, "2")) != "ok" || len(*started) != 2 || !(*started)[0].isKilled() || heals != 2 {
		t.Fatalf("%d backends, %d heals", len(*started), heals)
	}
	if ran := (*started)[1].ran(); len(ran) != 1 || ran[0] != p.tab {
		t.Fatalf("ran on %v, tab %s", ran, p.tab)
	}
}
