package sandboxapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/browserview"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/models"
	"agentrove/internal/store"

	"github.com/coder/websocket"
)

const (
	ownerID = "11111111-1111-1111-1111-111111111111"
	otherID = "22222222-2222-2222-2222-222222222222"
)

// hangingBox never gets its browser started, so a viewer stays in Ensure.
type hangingBox struct{}

func (hangingBox) Ensure(ctx context.Context) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}
func (hangingBox) Dial(context.Context) (io.ReadWriteCloser, error) { return nil, errors.New("no") }
func (hangingBox) Reset(context.Context) error                      { return nil }
func (hangingBox) Touch()                                           {}

type browserFixture struct {
	m   *Module
	srv *httptest.Server
	a   *app.App
}

func newBrowserFixture(t *testing.T) *browserFixture {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{APIPrefix: "/api/v1", AccessTokenTTL: time.Hour, BrowserEnabled: true}
	a := &app.App{Cfg: cfg, DB: d, Keys: keys, Store: &store.Store{DB: d, Keys: keys}}
	a.Auth = &auth.Service{DB: d, Cfg: cfg, Keys: keys}
	now := db.Now()
	for _, id := range []string{ownerID, otherID} {
		if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, ?, ?, 'x', 1, ?, ?)`,
			id, id+"@x", id, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path, created_at, updated_at)
		VALUES ('ws1', 'ws', ?, 'sb1', 'docker', '/tmp', ?, ?)`, ownerID, now, now); err != nil {
		t.Fatal(err)
	}
	m := &Module{a: a}
	m.browserBox = func(*models.Workspace) (browserview.Box, string, error) { return hangingBox{}, "box-1", nil }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/sandbox/{sandbox_id}/browser/ws", m.browserWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &browserFixture{m: m, srv: srv, a: a}
}

func (f *browserFixture) token(t *testing.T, userID string) string {
	t.Helper()
	tok, err := f.a.Auth.IssueAccessToken(userID)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// open connects and authenticates to sandbox sb.
func (f *browserFixture) open(t *testing.T, sb, token string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(f.srv.URL, "http")+"/api/v1/sandbox/"+sb+"/browser/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	b, _ := json.Marshal(map[string]any{"type": "auth", "token": token})
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
	return c
}

// closeOf reads until the server closes c.
func closeOf(t *testing.T, c *websocket.Conn) (websocket.StatusCode, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			var ce websocket.CloseError
			if errors.As(err, &ce) {
				return ce.Code, ce.Reason
			}
			t.Fatalf("read: %v", err)
		}
	}
}

// waitStarting reads until the server says it is starting the browser.
func waitStarting(t *testing.T, c *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if strings.Contains(string(b), `"starting"`) {
			return
		}
	}
}

func TestBrowserWSAuthAndOwnership(t *testing.T) {
	f := newBrowserFixture(t)
	if code, _ := closeOf(t, f.open(t, "sb1", "not-a-token")); code != wsCloseAuthFailed {
		t.Fatalf("bad token: %d", code)
	}
	// Another user's sandbox looks like none at all.
	if code, _ := closeOf(t, f.open(t, "sb1", f.token(t, otherID))); code != wsCloseSandboxNotFound {
		t.Fatalf("other user: %d", code)
	}
	waitStarting(t, f.open(t, "sb1", f.token(t, ownerID)))
}

func TestBrowserWSUnavailable(t *testing.T) {
	f := newBrowserFixture(t)
	f.a.Cfg.BrowserEnabled = false
	if code, reason := closeOf(t, f.open(t, "sb1", f.token(t, ownerID))); code != browserview.CloseUnavailable || !strings.Contains(reason, "turned off") {
		t.Fatalf("disabled: %d %q", code, reason)
	}
	f.a.Cfg.BrowserEnabled = true
	f.m.browserBox = func(*models.Workspace) (browserview.Box, string, error) { return nil, "", errors.New("host project") }
	if code, _ := closeOf(t, f.open(t, "sb1", f.token(t, ownerID))); code != browserview.CloseUnavailable {
		t.Fatalf("no box: %d", code)
	}
}

func TestBrowserWSViewerLimit(t *testing.T) {
	f := newBrowserFixture(t)
	tok := f.token(t, ownerID)
	var conns []*websocket.Conn
	for range maxBrowserViewers {
		c := f.open(t, "sb1", tok)
		waitStarting(t, c)
		conns = append(conns, c)
	}
	if code, _ := closeOf(t, f.open(t, "sb1", tok)); code != browserview.CloseTooMany {
		t.Fatalf("over the limit: %d", code)
	}
	// A viewer leaving frees its slot.
	_ = conns[0].Close(websocket.StatusNormalClosure, "")
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.m.viewers.mu.Lock()
		n := f.m.viewers.n["box-1"]
		f.m.viewers.mu.Unlock()
		if n < maxBrowserViewers {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot not released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitStarting(t, f.open(t, "sb1", tok))
}

// fakeProcess is a socat exec whose exit the test controls.
type fakeProcess struct {
	stdinClosed atomic.Bool
	killed      atomic.Bool
	exit        chan struct{}
	once        sync.Once
	stdout      io.Reader
}

type closeRecorder struct{ p *fakeProcess }

func (c closeRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (c closeRecorder) Close() error {
	c.p.stdinClosed.Store(true)
	return nil
}

func (p *fakeProcess) Stdin() io.WriteCloser { return closeRecorder{p} }
func (p *fakeProcess) Stdout() io.Reader     { return p.stdout }
func (p *fakeProcess) Stderr() io.Reader     { return strings.NewReader("") }
func (p *fakeProcess) Wait() error           { <-p.exit; return nil }
func (p *fakeProcess) Kill() error {
	p.killed.Store(true)
	p.once.Do(func() { close(p.exit) })
	return nil
}

func TestExecStreamClose(t *testing.T) {
	waitRelease := func(released chan struct{}) {
		t.Helper()
		select {
		case <-released:
		case <-time.After(3 * time.Second):
			t.Fatal("lease not released")
		}
	}
	// socat exits once its stdin closes: no kill.
	p := &fakeProcess{exit: make(chan struct{}), stdout: strings.NewReader("unread output")}
	released := make(chan struct{})
	s := &execStream{p: p, release: func() { close(released) }}
	_ = s.Close()
	_ = s.Close() // idempotent
	if !p.stdinClosed.Load() {
		t.Fatal("stdin not closed")
	}
	p.once.Do(func() { close(p.exit) })
	waitRelease(released)
	if p.killed.Load() {
		t.Fatal("killed a process that exited")
	}

	// One that hangs is killed.
	old := execStreamKillAfter
	execStreamKillAfter = 50 * time.Millisecond
	defer func() { execStreamKillAfter = old }()
	p = &fakeProcess{exit: make(chan struct{}), stdout: strings.NewReader("")}
	released = make(chan struct{})
	_ = (&execStream{p: p, release: func() { close(released) }}).Close()
	waitRelease(released)
	if !p.killed.Load() {
		t.Fatal("hung socat not killed")
	}
}
