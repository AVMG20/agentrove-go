package browserview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeBrowser is an in-memory Chromium for the CDP client: it answers the
// calls the viewer makes and emits the events it reacts to. It is a
// Transport itself.
type fakeBrowser struct {
	mu             sync.Mutex
	targets        []targetInfo
	calls          []wireMessage
	sessions       map[string]string // session -> target
	nextID         int
	failScreencast int
	// hold makes these methods never answer (a page stuck in a dialog).
	hold map[string]bool
	// poison is a URL whose tab sends more than cdpReadLimit when attached
	// (like a hostile page's history used to).
	poison string

	toClient  chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newFakeBrowser(pages ...string) *fakeBrowser {
	f := &fakeBrowser{sessions: map[string]string{}, hold: map[string]bool{}, toClient: make(chan []byte, 1024), closed: make(chan struct{})}
	for _, u := range pages {
		f.nextID++
		f.targets = append(f.targets, targetInfo{TargetID: fmt.Sprintf("T%d", f.nextID), Type: "page", URL: u, Title: u})
	}
	// Not a page: never shown as a tab.
	f.targets = append(f.targets, targetInfo{TargetID: "W1", Type: "service_worker", URL: "https://x/sw.js"})
	return f
}

func (f *fakeBrowser) ReadMessage(ctx context.Context) ([]byte, error) {
	select {
	case b := <-f.toClient:
		return b, nil
	case <-f.closed:
		return nil, io.EOF
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *fakeBrowser) Close() error {
	f.closeOnce.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeBrowser) send(v any) {
	b, _ := json.Marshal(v)
	select {
	case f.toClient <- b:
	case <-f.closed:
	}
}

func (f *fakeBrowser) emit(session, method string, params any) {
	raw, _ := json.Marshal(params)
	f.send(wireMessage{Method: method, SessionID: session, Params: raw})
}

// frame emits a screencast frame on session; its payload is "jpeg-<n>".
func (f *fakeBrowser) frame(session string, n int) {
	f.emit(session, "Page.screencastFrame", map[string]any{
		"data":      base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "jpeg-%d", n)),
		"sessionId": n,
		"metadata":  map[string]any{"deviceWidth": 1280, "deviceHeight": 800},
	})
}

func (f *fakeBrowser) WriteMessage(_ context.Context, msg []byte) error {
	var m wireMessage
	if err := json.Unmarshal(msg, &m); err != nil {
		return err
	}
	f.mu.Lock()
	f.calls = append(f.calls, m)
	hold := f.hold[m.Method]
	f.mu.Unlock()
	if hold {
		return nil
	}
	var params map[string]any
	_ = json.Unmarshal(m.Params, &params)
	result, cdpErr := f.handle(m, params)
	resp := wireMessage{ID: m.ID, Error: cdpErr}
	if cdpErr == nil {
		resp.Result, _ = json.Marshal(result)
	}
	f.send(resp)
	return nil
}

func (f *fakeBrowser) handle(m wireMessage, params map[string]any) (any, *CDPError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch m.Method {
	case "Target.getTargets":
		return map[string]any{"targetInfos": f.targets}, nil
	case "Target.setDiscoverTargets":
		// Chromium reports the existing targets too, before it answers.
		for _, t := range f.targets {
			f.emit("", "Target.targetCreated", map[string]any{"targetInfo": t})
		}
	case "Target.getTargetInfo":
		for _, t := range f.targets {
			if t.TargetID == params["targetId"] {
				return map[string]any{"targetInfo": t}, nil
			}
		}
		return nil, &CDPError{Code: -32602, Message: "No target with given id found"}
	case "Page.createIsolatedWorld":
		return map[string]any{"executionContextId": 1}, nil
	case "Runtime.evaluate":
		if params["expression"] == navStateScript {
			return map[string]any{"result": map[string]any{"type": "object", "value": []bool{true, true}}}, nil
		}
		return map[string]any{"result": map[string]any{"type": "undefined"}}, nil
	case "Target.attachToTarget":
		f.nextID++
		sid := fmt.Sprintf("S%d", f.nextID)
		f.sessions[sid] = params["targetId"].(string)
		for _, t := range f.targets {
			if t.TargetID == params["targetId"] && f.poison != "" && t.URL == f.poison {
				go f.emit(sid, "Page.huge", map[string]any{"data": strings.Repeat("a", cdpReadLimit+1)})
			}
		}
		return map[string]any{"sessionId": sid}, nil
	case "Target.createTarget":
		f.nextID++
		t := targetInfo{TargetID: fmt.Sprintf("T%d", f.nextID), Type: "page", URL: params["url"].(string)}
		f.targets = append(f.targets, t)
		go f.emit("", "Target.targetCreated", map[string]any{"targetInfo": t})
		return map[string]any{"targetId": t.TargetID}, nil
	case "Target.closeTarget":
		id := params["targetId"].(string)
		f.targets = slices.DeleteFunc(f.targets, func(t targetInfo) bool { return t.TargetID == id })
		go f.emit("", "Target.targetDestroyed", map[string]any{"targetId": id})
	case "Page.startScreencast":
		if f.failScreencast > 0 {
			f.failScreencast--
			return nil, &CDPError{Code: -32000, Message: "Not attached to an active page"}
		}
	case "Browser.getWindowForTarget":
		return map[string]any{"windowId": 7}, nil
	}
	return map[string]any{}, nil
}

// waitCall waits for the n-th (0-based) call of method and returns it.
func (f *fakeBrowser) waitCall(t *testing.T, method string, n int) wireMessage {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		seen := 0
		for _, c := range f.calls {
			if c.Method == method {
				if seen == n {
					f.mu.Unlock()
					return c
				}
				seen++
			}
		}
		f.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no call #%d of %s; calls: %v", n, method, f.methods())
	return wireMessage{}
}

func (f *fakeBrowser) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

func (f *fakeBrowser) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.Method)
	}
	return out
}

func params(m wireMessage) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(m.Params, &p)
	return p
}

// fakeSink records what the viewer sends the client.
type fakeSink struct {
	mu     sync.Mutex
	texts  []map[string]any
	frames [][]byte
}

func (s *fakeSink) Text(v any) {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	s.mu.Lock()
	s.texts = append(s.texts, m)
	s.mu.Unlock()
}

func (s *fakeSink) Frame(b []byte) {
	s.mu.Lock()
	s.frames = append(s.frames, b)
	s.mu.Unlock()
}

func (s *fakeSink) frameCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.frames)
}

// lastText waits for a message of type typ matching ok (nil: any) and returns
// the latest such message.
func (s *fakeSink) waitText(t *testing.T, typ string, ok func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for i := len(s.texts) - 1; i >= 0; i-- {
			if m := s.texts[i]; m["type"] == typ && (ok == nil || ok(m)) {
				s.mu.Unlock()
				return m
			}
		}
		s.mu.Unlock()
		time.Sleep(2 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Fatalf("no %s message; got %v", typ, s.texts)
	return nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	waitUntilFor(t, 2*time.Second, what, cond)
}

func waitUntilFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}
