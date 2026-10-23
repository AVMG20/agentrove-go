package sandboxapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentrove/internal/gitops"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Terminal WebSocket protocol (mirrors app/api/endpoints/websocket.py): the
// first frame is {"type":"auth","token":...}; then binary frames are PTY
// stdin and text frames are JSON control messages (init/refresh/resize/
// close/detach). Output goes out as {"type":"stdout","data":...}; a ping is
// sent after 30s without client traffic.
const (
	wsCloseAuthFailed      websocket.StatusCode = 4001
	wsCloseSandboxNotFound websocket.StatusCode = 4004
	wsCloseInvalidCwd      websocket.StatusCode = 4005

	wsAuthTimeout    = 10 * time.Second
	wsReceiveTimeout = 30 * time.Second

	defaultPtyRows    = 24
	defaultPtyCols    = 80
	defaultTerminalID = "terminal-1"

	ptyOutputQueueSize = 512
	ptyInputQueueSize  = 1024

	// A PTY nobody is attached to is closed after this grace period so it
	// stops pinning the sandbox awake; the tmux session survives inside the
	// sandbox and the next connect reattaches to it.
	detachGrace = 10 * time.Minute
)

// tmux's built-in update-environment list, kept when we override it.
var tmuxDefaultUpdateEnv = []string{"DISPLAY", "KRB5CCNAME", "SSH_ASKPASS", "SSH_AUTH_SOCK", "SSH_AGENT_PID", "SSH_CONNECTION", "WINDOWID", "XAUTHORITY"}

var tmuxNameUnsafeRE = regexp.MustCompile(`[^A-Za-z0-9]`)

// TmuxSessionName is stable per sandbox/terminal/cwd so reconnects reattach.
func TmuxSessionName(sandboxID, terminalID, cwd string) string {
	name := "agentrove_" + strings.ReplaceAll(sandboxID, "-", "_") + "_" + strings.ReplaceAll(terminalID, "-", "_")
	if cwd != "" {
		// Worktree terminals get their own session: reattaching a root session
		// would land the user outside the worktree.
		name += "_" + tmuxNameUnsafeRE.ReplaceAllString(cwd, "_")
	}
	return name
}

func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// BuildTmuxCommand launches (or reattaches) a tmux session, falling back to a
// bare shell when tmux is missing. Options keep tmux off the alternate screen
// so output reaches xterm's scrollback, forward OSC 52 copies, and pin
// session env (sessionEnv) / inherit secrets by name (inherit) without
// putting values in argv.
func BuildTmuxCommand(session, fallbackShell string, sessionEnv map[string]string, inherit []string) string {
	var envFlags strings.Builder
	keys := make([]string, 0, len(sessionEnv))
	for k := range sessionEnv {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		envFlags.WriteString(" -e " + shq(k+"="+sessionEnv[k]))
	}
	inheritPart := ""
	if len(inherit) > 0 {
		names := append(append([]string{}, tmuxDefaultUpdateEnv...), inherit...)
		inheritPart = ` \; set -g update-environment ` + shq(strings.Join(names, " "))
	}
	return "command -v tmux >/dev/null && " +
		"tmux set -g history-limit 10000" +
		inheritPart +
		` \; set -as terminal-overrides ',xterm-256color:smcup@:rmcup@:indn@'` +
		` \; new -A` + envFlags.String() + ` -s ` + shq(session) +
		` \; set -g status off` +
		` \; set -g mouse off` +
		` \; set -s set-clipboard on` +
		` \; set -as terminal-overrides ',xterm-256color:Ms=\E]52;%p1%s;%p2%s\007'` +
		` || exec ` + shq(fallbackShell)
}

// SplitUTF8 returns the longest valid-UTF-8 prefix of b as a string plus any
// trailing bytes of a rune cut off mid-sequence (to prepend to the next read).
func SplitUTF8(b []byte) (string, []byte) {
	cut := len(b)
	// A rune is at most 4 bytes: look back at most 3 for an unfinished start.
	for i := len(b) - 1; i >= 0 && i >= len(b)-3; i-- {
		c := b[i]
		if c < 0x80 {
			break
		}
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[i:]) {
				cut = i
			}
			break
		}
	}
	head := b[:cut]
	rest := append([]byte(nil), b[cut:]...)
	if utf8.Valid(head) {
		return string(head), rest
	}
	return strings.ToValidUTF8(string(head), "�"), rest
}

// ParseDimension mirrors parse_pty_dimension: only JSON integers count.
func ParseDimension(raw json.RawMessage, def, lo, hi int) int {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || !(raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9')) {
		return def
	}
	var n json.Number
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if dec.Decode(&n) != nil {
		return def
	}
	if strings.ContainsAny(n.String(), ".eE") {
		return def
	}
	v, err := n.Int64()
	if err != nil {
		return def
	}
	return int(max(int64(lo), min(v, int64(hi))))
}

type terminals struct {
	mgr      *sandbox.Manager
	mu       sync.Mutex
	sessions map[string]*termSession
}

func newTerminals(mgr *sandbox.Manager) *terminals {
	return &terminals{mgr: mgr, sessions: map[string]*termSession{}}
}

type termSession struct {
	reg        *terminals
	key        string
	sb         *sandbox.Sandbox
	sandboxID  string
	terminalID string
	cwd        string

	startMu sync.Mutex

	mu        sync.Mutex
	pty       sandbox.PTY
	release   func()
	ptyID     string
	out       chan string
	in        chan []byte
	stop      chan struct{}
	active    *websocket.Conn
	fwdCancel context.CancelFunc
	idle      *time.Timer
}

func (t *terminals) getOrCreate(userID string, ws *models.Workspace, terminalID, cwd string) (*termSession, error) {
	// cwd is part of the identity: a worktree chat must not reattach to a
	// root-cwd session or vice versa.
	key := userID + ":" + ws.SandboxID + ":" + terminalID + ":" + cwd
	t.mu.Lock()
	defer t.mu.Unlock()
	if s, ok := t.sessions[key]; ok {
		return s, nil
	}
	sb, err := t.mgr.For(ws)
	if err != nil {
		return nil, err
	}
	s := &termSession{reg: t, key: key, sb: sb, sandboxID: ws.SandboxID, terminalID: terminalID, cwd: cwd}
	t.sessions[key] = s
	return s, nil
}

func (t *terminals) remove(s *termSession) {
	t.mu.Lock()
	if t.sessions[s.key] == s {
		delete(t.sessions, s.key)
	}
	t.mu.Unlock()
}

// terminateSandbox kills every terminal (and its tmux session) of a sandbox;
// used before the sandbox itself is deleted.
func (t *terminals) terminateSandbox(ctx context.Context, sandboxID string) {
	t.mu.Lock()
	var list []*termSession
	for _, s := range t.sessions {
		if s.sandboxID == sandboxID {
			list = append(list, s)
		}
	}
	t.mu.Unlock()
	for _, s := range list {
		s.terminate(ctx)
	}
}

// closeAll closes every PTY (tmux sessions survive for the next start).
func (t *terminals) closeAll() {
	t.mu.Lock()
	list := make([]*termSession, 0, len(t.sessions))
	for _, s := range t.sessions {
		list = append(list, s)
	}
	t.mu.Unlock()
	for _, s := range list {
		s.close()
	}
}

func (s *termSession) tmuxName() string { return TmuxSessionName(s.sandboxID, s.terminalID, s.cwd) }

func (s *termSession) ptyRequest() sandbox.ExecRequest {
	env := s.sb.Env()
	var inherit []string
	for k := range env {
		if k != "TERM" && k != "HOME" {
			inherit = append(inherit, k)
		}
	}
	slices.Sort(inherit)
	if s.sb.Kind == sandbox.KindDocker {
		// Root terminals start in $HOME (dotfiles, existing behavior);
		// worktree terminals start inside the worktree.
		cwd := sandbox.ContainerHome
		if s.cwd != "" {
			cwd = s.sb.Resolve(s.cwd)
		}
		return sandbox.ExecRequest{Cmd: []string{"bash", "-c", BuildTmuxCommand(s.tmuxName(), "bash", nil, inherit)}, Cwd: cwd}
	}
	// Pin the per-user agent HOME at the tmux session level: a client's env
	// never reaches shells of an already-running tmux server.
	sessionEnv := map[string]string{"HOME": s.sb.Home}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	return sandbox.ExecRequest{Cmd: []string{"bash", "-lc", BuildTmuxCommand(s.tmuxName(), shell, sessionEnv, inherit)}, Cwd: s.sb.Resolve(s.cwd)}
}

// ensureStarted spawns the PTY once (serialized: a page refresh can race two
// connections into the same session) or resizes the running one.
func (s *termSession) ensureStarted(rows, cols int) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.mu.Lock()
	running := s.pty != nil
	s.mu.Unlock()
	if running {
		s.resize(rows, cols)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, release, err := s.sb.StartPTY(ctx, s.ptyRequest(), rows, cols)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.pty, s.release, s.ptyID = p, release, uuid.NewString()
	s.out = make(chan string, ptyOutputQueueSize)
	s.in = make(chan []byte, ptyInputQueueSize)
	s.stop = make(chan struct{})
	out, in, stop := s.out, s.in, s.stop
	s.mu.Unlock()
	go s.readLoop(p, out)
	go s.inputLoop(p, in, stop)
	return nil
}

func (s *termSession) resize(rows, cols int) {
	s.mu.Lock()
	p := s.pty
	s.mu.Unlock()
	if p == nil {
		return
	}
	if err := p.Resize(rows, cols); err != nil {
		slog.Debug("terminal resize", "sandbox", s.sandboxID, "err", err)
	}
}

// pushDropOldest enqueues without blocking, dropping the oldest item when full.
func pushDropOldest[T any](ch chan T, v T) {
	for range 2 {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

func (s *termSession) readLoop(p sandbox.PTY, out chan string) {
	buf := make([]byte, 32*1024)
	var pending []byte
	for {
		n, err := p.Read(buf)
		if n > 0 {
			text, rest := SplitUTF8(append(pending, buf[:n]...))
			pending = rest
			if text != "" {
				pushDropOldest(out, text)
				s.sb.Touch()
			}
		}
		if err != nil {
			break
		}
	}
	s.onExit(p)
}

func (s *termSession) inputLoop(p sandbox.PTY, in chan []byte, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case first := <-in:
			payload := append([]byte(nil), first...)
		drain:
			for {
				select {
				case more := <-in:
					payload = append(payload, more...)
				default:
					break drain
				}
			}
			if _, err := p.Write(payload); err != nil {
				slog.Debug("terminal input", "sandbox", s.sandboxID, "err", err)
				s.close()
				return
			}
		}
	}
}

func (s *termSession) enqueueInput(b []byte) {
	s.mu.Lock()
	in := s.in
	s.mu.Unlock()
	if in != nil {
		pushDropOldest(in, b)
	}
}

func forward(ctx context.Context, c *websocket.Conn, out chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case first := <-out:
			var b strings.Builder
			b.WriteString(first)
		drain:
			for {
				select {
				case more := <-out:
					b.WriteString(more)
				default:
					break drain
				}
			}
			msg, _ := json.Marshal(map[string]string{"type": "stdout", "data": b.String()})
			if err := c.Write(ctx, websocket.MessageText, msg); err != nil {
				return
			}
		}
	}
}

// attach makes c the session's only viewer, closing any previous socket.
func (s *termSession) attach(c *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil && s.active != c {
		old := s.active
		go old.Close(websocket.StatusNormalClosure, "")
	}
	s.active = c
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	if s.fwdCancel != nil {
		s.fwdCancel()
		s.fwdCancel = nil
	}
	if s.pty == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.fwdCancel = cancel
	go forward(ctx, c, s.out)
}

func (s *termSession) detach(c *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != c {
		return
	}
	s.active = nil
	if s.fwdCancel != nil {
		s.fwdCancel()
		s.fwdCancel = nil
	}
	if s.pty != nil && s.idle == nil {
		s.idle = time.AfterFunc(detachGrace, func() {
			s.mu.Lock()
			unattended := s.active == nil
			s.mu.Unlock()
			if unattended {
				s.close()
			}
		})
	}
}

// close tears down the PTY and forgets the session (tmux keeps running).
func (s *termSession) close() {
	s.mu.Lock()
	p, release := s.pty, s.release
	s.pty, s.release, s.ptyID = nil, nil, ""
	s.active = nil
	if s.fwdCancel != nil {
		s.fwdCancel()
		s.fwdCancel = nil
	}
	if s.idle != nil {
		s.idle.Stop()
		s.idle = nil
	}
	if s.stop != nil {
		close(s.stop)
		s.stop = nil
	}
	s.in, s.out = nil, nil
	s.mu.Unlock()
	s.reg.remove(s)
	if p != nil {
		_ = p.Close()
		go func() { _ = p.Wait() }()
	}
	if release != nil {
		release()
	}
}

// onExit handles the shell/tmux dying on its own: drop the session so the next
// connect starts fresh, and close the viewer so it can offer a reconnect.
func (s *termSession) onExit(p sandbox.PTY) {
	s.mu.Lock()
	current := s.pty == p
	c := s.active
	s.mu.Unlock()
	if !current {
		return
	}
	s.close()
	if c != nil {
		_ = c.Close(websocket.StatusNormalClosure, "")
	}
}

func (s *termSession) terminate(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, _ = s.sb.Shell(ctx, "", "tmux kill-session -t "+shq(s.tmuxName())+" 2>/dev/null", 20*time.Second)
	s.close()
}

// refresh repaints reattached clients through tmux itself: Ctrl-L to the pane
// only redraws shells, full-screen TUIs would swallow it.
func (s *termSession) refresh(ctx context.Context) {
	s.mu.Lock()
	running := s.pty != nil
	s.mu.Unlock()
	if !running {
		return
	}
	name := shq(s.tmuxName())
	script := "for c in $(tmux list-clients -t " + name + " -F '#{client_name}'); do tmux refresh-client -t \"$c\"; done"
	_, _ = s.sb.Shell(ctx, "", script, 15*time.Second)
}

type wsInbound struct {
	typ  websocket.MessageType
	data []byte
}

func (m *Module) terminalWS(w http.ResponseWriter, r *http.Request) {
	// Auth is the first frame (browsers can't set WS headers), so the
	// handshake itself is open; origin checks add nothing to a token flow.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	// Unauthenticated peers only get room for the auth frame.
	c.SetReadLimit(64 << 10)
	ctx := r.Context()

	actx, cancel := context.WithTimeout(ctx, wsAuthTimeout)
	_, first, err := c.Read(actx)
	cancel()
	var user *models.User
	if err == nil {
		var msg struct {
			Type  string `json:"type"`
			Token any    `json:"token"`
		}
		if json.Unmarshal(first, &msg) == nil && msg.Type == "auth" {
			if tok, ok := msg.Token.(string); ok && tok != "" {
				user, _ = m.a.Auth.Authenticate(ctx, tok)
			}
		}
	}
	if user == nil {
		_ = c.Close(wsCloseAuthFailed, "Authentication failed")
		return
	}
	c.SetReadLimit(8 << 20)
	sandboxID := r.PathValue("sandbox_id")
	ws, err := m.a.Store.GetWorkspaceBySandbox(ctx, user.ID, sandboxID)
	if err != nil {
		_ = c.Close(wsCloseSandboxNotFound, "Sandbox not found")
		return
	}
	cwd, err := gitops.NormalizeRel(r.URL.Query().Get("cwd"))
	if err != nil {
		_ = c.Close(wsCloseInvalidCwd, "Invalid terminal cwd")
		return
	}
	terminalID := r.URL.Query().Get("terminalId")
	if terminalID == "" {
		terminalID = defaultTerminalID
	}
	session, err := m.terms.getOrCreate(user.ID, ws, terminalID, cwd)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "Sandbox unavailable")
		return
	}

	inbound := make(chan wsInbound, 64)
	go func() {
		defer close(inbound)
		for {
			typ, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			select {
			case inbound <- wsInbound{typ, data}:
			case <-ctx.Done():
				return
			}
		}
	}()

	defer func() {
		session.detach(c)
		_ = c.Close(websocket.StatusNormalClosure, "")
	}()

	timer := time.NewTimer(wsReceiveTimeout)
	defer timer.Stop()
	for {
		var msg wsInbound
		var ok bool
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			// NAT/LB keepalive; no pong expected.
			if err := c.Write(ctx, websocket.MessageText, []byte(`{"type": "ping"}`)); err != nil {
				return
			}
			timer.Reset(wsReceiveTimeout)
			continue
		case msg, ok = <-inbound:
			if !ok {
				return
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wsReceiveTimeout)

		if msg.typ == websocket.MessageBinary {
			session.enqueueInput(msg.data)
			continue
		}
		var ctl map[string]json.RawMessage
		if json.Unmarshal(msg.data, &ctl) != nil {
			continue
		}
		var typ string
		if json.Unmarshal(ctl["type"], &typ) != nil || typ == "" {
			continue
		}
		switch typ {
		case "init":
			rows := ParseDimension(ctl["rows"], defaultPtyRows, 1, 500)
			cols := ParseDimension(ctl["cols"], defaultPtyCols, 1, 500)
			if err := session.ensureStarted(rows, cols); err != nil {
				slog.Warn("start terminal", "sandbox", sandboxID, "err", err)
				_ = c.Close(websocket.StatusInternalError, "Failed to start terminal")
				return
			}
			session.attach(c)
			session.mu.Lock()
			id := session.ptyID
			session.mu.Unlock()
			out, _ := json.Marshal(map[string]any{"type": "init", "id": id, "rows": rows, "cols": cols})
			if err := c.Write(ctx, websocket.MessageText, out); err != nil {
				return
			}
		case "refresh":
			session.refresh(ctx)
		case "resize":
			rows := ParseDimension(ctl["rows"], 0, 0, 500)
			cols := ParseDimension(ctl["cols"], 0, 0, 500)
			if rows > 0 && cols > 0 {
				session.resize(rows, cols)
			}
		case "close":
			session.terminate(context.WithoutCancel(ctx))
			return
		case "detach":
			return
		}
	}
}
