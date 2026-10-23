package sandboxapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentrove/internal/auth"
	"agentrove/internal/browserview"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"

	"github.com/coder/websocket"
)

// Viewers per box: each holds a docker exec, a CDP session and a screencast.
const maxBrowserViewers = 4

// browserWS is the live viewer of the box's shared browser. Auth is the
// first frame, as for the terminal; then browserview's protocol.
func (m *Module) browserWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	ctx := r.Context()
	user := m.wsAuthenticate(ctx, c)
	if user == nil {
		return
	}
	ws, err := m.a.Store.GetWorkspaceBySandbox(ctx, user.ID, r.PathValue("sandbox_id"))
	if err != nil {
		_ = c.Close(wsCloseSandboxNotFound, "Sandbox not found")
		return
	}
	if !m.a.Cfg.BrowserEnabled {
		_ = c.Close(browserview.CloseUnavailable, "The browser is turned off on this server")
		return
	}
	box, boxID, err := m.browserBox(ws)
	if err != nil {
		_ = c.Close(browserview.CloseUnavailable, "This project has no box browser")
		return
	}
	if !m.viewers.add(boxID) {
		_ = c.Close(browserview.CloseTooMany, "Too many browser viewers open")
		return
	}
	defer m.viewers.done(boxID)
	browserview.Serve(ctx, c, box)
}

// browserReset stops the box browser and wipes its profile without a
// viewer session, so the viewer can recover even when it can't connect.
func (m *Module) browserReset(w http.ResponseWriter, r *http.Request) error {
	ws, err := m.a.Store.GetWorkspaceBySandbox(r.Context(), auth.UserFrom(r.Context()).ID, r.PathValue("sandbox_id"))
	if err != nil {
		return err
	}
	if !m.a.Cfg.BrowserEnabled {
		return httpx.BadRequest("The browser is turned off on this server")
	}
	box, _, err := m.browserBox(ws)
	if err != nil {
		return httpx.BadRequest("This project has no box browser")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := box.Reset(ctx); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// dockerBrowser is the browser of the workspace's Docker box (Module.browserBox).
func (m *Module) dockerBrowser(ws *models.Workspace) (browserview.Box, string, error) {
	sb, err := m.a.Sandboxes.For(ws)
	if err != nil {
		return nil, "", err
	}
	if sb.Kind != sandbox.KindDocker {
		return nil, "", fmt.Errorf("not a Docker box")
	}
	return &boxBrowser{sb: sb, idle: m.a.Cfg.BrowserIdleTTL}, sb.ID, nil
}

type viewerCount struct {
	mu sync.Mutex
	n  map[string]int
}

func (v *viewerCount) add(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.n == nil {
		v.n = map[string]int{}
	}
	if v.n[id] >= maxBrowserViewers {
		return false
	}
	v.n[id]++
	return true
}

func (v *viewerCount) done(id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.n[id]--; v.n[id] <= 0 {
		delete(v.n, id)
	}
}

// boxBrowser reaches the box's browser through docker exec: the launcher
// for start/reset, and socat as a byte stream to the loopback CDP port (the
// API can't connect to a box over the network).
type boxBrowser struct {
	sb   *sandbox.Sandbox
	idle time.Duration
}

func (b *boxBrowser) launcher(ctx context.Context, cmd string) (string, error) {
	res, err := b.sb.Exec(ctx, sandbox.ExecRequest{
		Cmd:     []string{"agentrove-browser", cmd},
		Cwd:     sandbox.ContainerHome,
		Env:     map[string]string{"AGENTROVE_BROWSER_IDLE_SECONDS": strconv.Itoa(int(b.idle.Seconds()))},
		Timeout: 60 * time.Second,
	})
	if err != nil {
		return "", err
	}
	switch res.ExitCode {
	case 0:
		return strings.TrimSpace(res.Stdout), nil
	case 126, 127:
		return "", fmt.Errorf("this box image has no browser; rebuild the sandbox image")
	}
	return "", fmt.Errorf("agentrove-browser %s: %s", cmd, lastLine(res.Output()))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (b *boxBrowser) Ensure(ctx context.Context) (string, error) {
	path, err := b.launcher(ctx, "ensure")
	if err != nil {
		return "", err
	}
	path = lastLine(path)
	if !strings.HasPrefix(path, "/devtools/browser/") {
		return "", fmt.Errorf("unexpected CDP endpoint %q", path)
	}
	return path, nil
}

func (b *boxBrowser) Reset(ctx context.Context) error {
	_, err := b.launcher(ctx, "reset")
	return err
}

func (b *boxBrowser) Touch() { b.sb.Touch() }

// Dial starts socat in the box. The process holds a lease, so the box isn't
// stopped while someone is watching.
func (b *boxBrowser) Dial(ctx context.Context) (io.ReadWriteCloser, error) {
	p, release, err := b.sb.Start(ctx, sandbox.ExecRequest{
		Cmd: []string{"socat", "STDIO", "TCP:127.0.0.1:" + strconv.Itoa(browserview.DebugPort)},
		Cwd: sandbox.ContainerHome,
	})
	if err != nil {
		return nil, err
	}
	// Unread stderr would block the exec's demultiplexer, stdout included.
	go func() { _, _ = io.Copy(io.Discard, p.Stderr()) }()
	return &execStream{p: p, release: release}, nil
}

// execStreamKillAfter is how long socat gets to exit after its stdin closes.
var execStreamKillAfter = 3 * time.Second

type execStream struct {
	p       sandbox.Process
	release func()
	once    sync.Once
}

func (s *execStream) Read(b []byte) (int, error)  { return s.p.Stdout().Read(b) }
func (s *execStream) Write(b []byte) (int, error) { return s.p.Stdin().Write(b) }

// Close ends socat by closing its stdin, which closes the CDP connection;
// it is killed if that doesn't end it.
func (s *execStream) Close() error {
	s.once.Do(func() {
		_ = s.p.Stdin().Close()
		go func() { _, _ = io.Copy(io.Discard, s.p.Stdout()) }()
		go func() {
			done := make(chan struct{})
			go func() { _ = s.p.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(execStreamKillAfter):
				_ = s.p.Kill()
			}
			s.release()
		}()
	})
	return nil
}
