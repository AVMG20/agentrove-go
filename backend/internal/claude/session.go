// Package claude drives the Claude Code CLI over its stream-json protocol:
// one long-lived `claude -p --input-format stream-json` process per chat,
// with permission prompts answered through stdio control requests.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentrove/internal/sandbox"

	"github.com/google/uuid"
)

// Message is one raw stdout line from the CLI.
type Message struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype,omitempty"`
	SessionID       string          `json:"session_id,omitempty"`
	ParentToolUseID *string         `json:"parent_tool_use_id,omitempty"`
	Event           json.RawMessage `json:"event,omitempty"`
	Message         json.RawMessage `json:"message,omitempty"`
	ToolUseResult   json.RawMessage `json:"tool_use_result,omitempty"`
	RequestID       string          `json:"request_id,omitempty"`
	Request         json.RawMessage `json:"request,omitempty"`
	Response        json.RawMessage `json:"response,omitempty"`
	UUID            string          `json:"uuid,omitempty"`
	IsReplay        bool            `json:"isReplay,omitempty"`
	Raw             json.RawMessage `json:"-"`
}

// Config is everything that determines how the CLI process is launched.
// Changing any launch-only field requires a respawn (resuming the session).
type Config struct {
	Model          string
	PermissionMode string
	Effort         string
	Cwd            string // absolute path in the sandbox
	SystemPrompt   string
	FullReplace    bool // persona replaces Claude Code's system prompt
	ResumeID       string
	MCPServers     map[string]any
	Settings       map[string]any
	Env            map[string]string
	// Unset drops inherited env vars (a profile's foreign credentials).
	Unset []string
	// Setup runs in the box shell before the CLI (a profile's dirs).
	Setup  string
	Binary string
}

// fingerprint covers the launch-only fields (model and permission mode are
// switched live via control requests).
func (c Config) fingerprint() string {
	b, _ := json.Marshal(struct {
		Cwd, Prompt, Effort string
		Full                bool
		MCP                 map[string]any
		Settings            map[string]any
		Env                 map[string]string
		Unset               []string
		Setup               string
	}{c.Cwd, c.SystemPrompt, c.Effort, c.FullReplace, c.MCPServers, c.Settings, c.Env, c.Unset, c.Setup})
	return string(b)
}

func (c Config) args() []string {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--input-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--permission-prompt-tool", "stdio",
		"--thinking", "adaptive",
		"--thinking-display", "summarized",
		"--setting-sources", "user,project,local",
		// Echo each user message once the CLI starts on it: a turn ends only
		// on the result after its own echo.
		"--replay-user-messages",
		// Use the system prompt passed at launch, not the one snapshotted when
		// the session began, so persona changes apply on resume.
		"--system-prompt-snapshot", "off",
		// Always allowed so a session launched in another mode can still be
		// switched to bypassPermissions later.
		"--allow-dangerously-skip-permissions",
	}
	if c.PermissionMode != "" {
		args = append(args, "--permission-mode", c.PermissionMode)
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.Effort != "" {
		args = append(args, "--effort", c.Effort)
	}
	if c.ResumeID != "" {
		args = append(args, "--resume", c.ResumeID)
	}
	if c.SystemPrompt != "" {
		if c.FullReplace {
			args = append(args, "--system-prompt", c.SystemPrompt)
		} else {
			args = append(args, "--append-system-prompt", c.SystemPrompt)
		}
	}
	if len(c.Settings) > 0 {
		b, _ := json.Marshal(c.Settings)
		args = append(args, "--settings", string(b))
	}
	if len(c.MCPServers) > 0 {
		b, _ := json.Marshal(map[string]any{"mcpServers": c.MCPServers})
		args = append(args, "--mcp-config", string(b))
	}
	return args
}

// Session is a running CLI process bound to one chat.
type Session struct {
	ChatID      string
	SandboxID   string
	fingerprint string

	proc    sandbox.Process
	release func()

	writeMu sync.Mutex
	stdin   io.WriteCloser

	mu        sync.Mutex
	sessionID string
	lastCost  float64
	model     string
	mode      string
	effort    string
	pending   map[string]chan controlResult // our outgoing control requests
	sink      func(Message)                 // receives every non-control message
	closed    bool
	lastUsed  time.Time
	initInfo  json.RawMessage

	// Incoming can_use_tool requests are handed to this callback.
	onControl func(requestID string, req json.RawMessage)

	reqSeq  atomic.Int64
	done    chan struct{}
	exitErr error
	stderr  *tailBuffer
}

type controlResult struct {
	resp json.RawMessage
	err  error
}

// outputDrainTimeout bounds how long exit handling waits for the readers to
// hand on the process's last output (a grandchild can keep stdout open).
// A var so tests can shorten it.
var outputDrainTimeout = 5 * time.Second

// Start launches the CLI in sb and performs the initialize handshake.
func Start(ctx context.Context, sb *sandbox.Sandbox, chatID string, cfg Config) (*Session, error) {
	bin := cfg.Binary
	if bin == "" {
		bin = "claude"
	}
	cmd := append([]string{bin}, cfg.args()...)
	if sb.Kind == sandbox.KindDocker {
		// Login shell so ~/.bashrc PATH (npm/nvm/native install) resolves claude.
		script := `exec "$@"`
		if cfg.Setup != "" {
			script = cfg.Setup + ` && exec "$@"`
		}
		cmd = []string{"bash", "-lc", script, "claude"}
		cmd = append(cmd, append([]string{bin}, cfg.args()...)...)
	}
	proc, release, err := sb.Start(ctx, sandbox.ExecRequest{Cmd: cmd, Cwd: cfg.Cwd, Env: cfg.Env, Unset: cfg.Unset})
	if err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	return start(ctx, proc, release, sb.ID, chatID, cfg)
}

func start(ctx context.Context, proc sandbox.Process, release func(), sandboxID, chatID string, cfg Config) (*Session, error) {
	s := &Session{
		ChatID:      chatID,
		SandboxID:   sandboxID,
		fingerprint: cfg.fingerprint(),
		proc:        proc,
		release:     release,
		stdin:       proc.Stdin(),
		sessionID:   cfg.ResumeID,
		model:       cfg.Model,
		mode:        cfg.PermissionMode,
		effort:      cfg.Effort,
		pending:     map[string]chan controlResult{},
		lastUsed:    time.Now(),
		done:        make(chan struct{}),
		stderr:      &tailBuffer{max: 8192},
	}
	stdoutDone, stderrDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(stderrDone); _, _ = io.Copy(s.stderr, proc.Stderr()) }()
	go func() { defer close(stdoutDone); s.readLoop(proc.Stdout()) }()
	go func() {
		err := proc.Wait()
		// Wait can return before the readers have handled the last lines (the
		// turn's result among them, and the stderr tail deathError reports).
		// Done must not fire ahead of that output.
		drain := time.NewTimer(outputDrainTimeout)
	wait:
		for _, ch := range []chan struct{}{stdoutDone, stderrDone} {
			select {
			case <-ch:
			case <-drain.C:
				slog.Warn("claude exited but its output did not close", "chat", s.ChatID)
				break wait
			}
		}
		drain.Stop()
		s.mu.Lock()
		s.exitErr = err
		s.closed = true
		pending := s.pending
		s.pending = map[string]chan controlResult{}
		s.mu.Unlock()
		for _, ch := range pending {
			ch <- controlResult{err: s.deathError()}
		}
		release()
		close(s.done)
	}()

	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	resp, err := s.Control(initCtx, map[string]any{"subtype": "initialize", "hooks": nil})
	if err != nil {
		s.Close()
		return nil, fmt.Errorf("claude initialize: %w", err)
	}
	s.mu.Lock()
	s.initInfo = resp
	s.mu.Unlock()
	return s, nil
}

func (s *Session) deathError() error {
	tail := strings.TrimSpace(s.stderr.String())
	if tail != "" {
		return fmt.Errorf("claude exited: %s", lastLines(tail, 6))
	}
	if s.exitErr != nil {
		return fmt.Errorf("claude exited: %v", s.exitErr)
	}
	return errors.New("claude exited")
}

func (s *Session) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(line)
		}
		if err != nil {
			return
		}
	}
}

// parseLine decodes one stdout line; ok is false for blank or non-JSON lines.
func parseLine(line []byte) (m Message, ok bool, err error) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 || line[0] != '{' {
		return m, false, nil
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return m, false, err
	}
	m.Raw = line
	return m, true, nil
}

func (s *Session) handleLine(line []byte) {
	m, ok, err := parseLine(line)
	if err != nil {
		slog.Debug("claude: unparsable line", "chat", s.ChatID, "err", err)
	}
	if !ok {
		return
	}
	switch m.Type {
	case "control_response":
		var env struct {
			Subtype   string          `json:"subtype"`
			RequestID string          `json:"request_id"`
			Response  json.RawMessage `json:"response"`
			Error     string          `json:"error"`
		}
		_ = json.Unmarshal(m.Response, &env)
		s.mu.Lock()
		ch := s.pending[env.RequestID]
		delete(s.pending, env.RequestID)
		s.mu.Unlock()
		if ch != nil {
			if env.Subtype == "error" {
				ch <- controlResult{err: errors.New(env.Error)}
			} else {
				ch <- controlResult{resp: env.Response}
			}
		}
		return
	case "control_request":
		s.mu.Lock()
		cb := s.onControl
		s.mu.Unlock()
		if cb != nil {
			cb(m.RequestID, m.Request)
		} else {
			// Nobody is listening (between turns): deny so the CLI never hangs.
			s.RespondControl(m.RequestID, map[string]any{"behavior": "deny", "message": "No active turn"})
		}
		return
	case "control_cancel_request":
		// The CLI withdrew a pending request (e.g. the turn was interrupted):
		// pass it on so its prompt is dismissed.
		s.mu.Lock()
		cb := s.onControl
		s.mu.Unlock()
		if cb != nil && m.RequestID != "" {
			cb(m.RequestID, CancelControl)
		}
		return
	}
	if m.SessionID != "" {
		s.mu.Lock()
		s.sessionID = m.SessionID
		s.mu.Unlock()
	}
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		sink(m)
	}
}

func (s *Session) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.Closed() {
		return s.deathError()
	}
	_, err = s.stdin.Write(b)
	return err
}

// Control sends a control request and waits for its response.
func (s *Session) Control(ctx context.Context, req map[string]any) (json.RawMessage, error) {
	id := fmt.Sprintf("req_%d", s.reqSeq.Add(1))
	ch := make(chan controlResult, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, s.deathError()
	}
	s.pending[id] = ch
	s.mu.Unlock()
	if err := s.write(map[string]any{"type": "control_request", "request_id": id, "request": req}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case r := <-ch:
		return r.resp, r.err
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, ctx.Err()
	}
}

// RespondControl answers a can_use_tool (or other) request from the CLI.
func (s *Session) RespondControl(requestID string, response map[string]any) {
	_ = s.write(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": requestID,
			"response":   response,
		},
	})
}

// SendUser writes a user message (text and/or content blocks).
// SendUser sends a prompt and returns the uuid the CLI echoes back when it
// starts processing it.
func (s *Session) SendUser(content []map[string]any) (string, error) {
	s.Touch()
	id := uuid.NewString()
	return id, s.write(map[string]any{
		"type":               "user",
		"uuid":               id,
		"message":            map[string]any{"role": "user", "content": content},
		"parent_tool_use_id": nil,
		"session_id":         s.SessionID(),
	})
}

// CostDelta turns the CLI's cumulative total_cost_usd into this result's share.
func (s *Session) CostDelta(total float64) float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := total - s.lastCost
	s.lastCost = total
	if d < 0 {
		return total
	}
	return d
}

func (s *Session) Interrupt(ctx context.Context) error {
	_, err := s.Control(ctx, map[string]any{"subtype": "interrupt"})
	return err
}

func (s *Session) SetModel(ctx context.Context, model string) error {
	s.mu.Lock()
	same := s.model == model
	s.mu.Unlock()
	if same || model == "" {
		return nil
	}
	if _, err := s.Control(ctx, map[string]any{"subtype": "set_model", "model": model}); err != nil {
		return err
	}
	s.mu.Lock()
	s.model = model
	s.mu.Unlock()
	return nil
}

func (s *Session) SetPermissionMode(ctx context.Context, mode string) error {
	s.mu.Lock()
	same := s.mode == mode
	s.mu.Unlock()
	if same || mode == "" {
		return nil
	}
	if _, err := s.Control(ctx, map[string]any{"subtype": "set_permission_mode", "mode": mode}); err != nil {
		return err
	}
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
	return nil
}

// NoteMode records a mode change the CLI made itself (plan mode transitions).
func (s *Session) NoteMode(mode string) {
	s.mu.Lock()
	s.mode = mode
	s.mu.Unlock()
}

func (s *Session) StopTask(ctx context.Context, taskID string) error {
	_, err := s.Control(ctx, map[string]any{"subtype": "stop_task", "task_id": taskID})
	return err
}

// Attach routes output messages and control requests to the active turn.
func (s *Session) Attach(sink func(Message), onControl func(string, json.RawMessage)) {
	s.mu.Lock()
	s.sink = sink
	s.onControl = onControl
	s.mu.Unlock()
}

// Matches reports whether the process was launched with cfg's launch-only
// settings (model and permission mode are switched live instead).
func (s *Session) Matches(cfg Config) bool { return s.fingerprint == cfg.fingerprint() }

func (s *Session) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

func (s *Session) Model() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.model
}

func (s *Session) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Session) InitInfo() json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initInfo
}

func (s *Session) Touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *Session) IdleFor() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Since(s.lastUsed)
}

func (s *Session) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) Err() error { return s.deathError() }

// Close ends the process: closing stdin lets the CLI flush and exit; it is
// killed if it lingers.
func (s *Session) Close() {
	s.writeMu.Lock()
	_ = s.stdin.Close()
	s.writeMu.Unlock()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		_ = s.proc.Kill()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
			s.release()
		}
	}
}

type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
