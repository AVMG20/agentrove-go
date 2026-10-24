// Package chat owns chats, messages, the streaming turn runtime and the
// Claude session registry.
package chat

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/prompts"
	"agentrove/internal/sandbox"

	"github.com/google/uuid"
)

const DefaultPersona = "Default"

type Module struct {
	a        *app.App
	st       *store
	sessions *sessions

	mu       sync.Mutex
	turns    map[string]*turn // running (or handing off) turn per chat
	attached map[string]*turn // turn currently consuming session output
	starting map[string]bool
	bgTasks  map[string][]backgroundTask
	bgOwner  map[string]string // chat -> user, for publishing task changes
	// live is the session whose output and background tasks the chat follows;
	// a session replaced by a respawn still runs until its Close finishes.
	live map[string]*claude.Session
	// stopStarting marks a reserved chat whose turn was stopped before it
	// launched (e.g. while the box woke up); it then launches only to finish
	// as interrupted.
	stopStarting map[string]bool

	// routeMu orders session output against turns attaching and detaching:
	// output that arrives while no turn is attached is held in pending and
	// handed to the next turn that attaches, so a self-started cycle loses
	// nothing. Nothing blocks while it is held.
	routeMu sync.Mutex
	pending map[string][]claude.Message

	// runTurn is startTurn; queue hand-offs go through it so tests can
	// observe them without a sandbox.
	runTurn func(context.Context, startInput) (app.ChatTurnResult, error)

	// MCPConfig returns the mcpServers map for a chat's Claude session. It
	// must be stable across turns: the session fingerprint includes it.
	MCPConfig func(userID, chatID, sandboxKind string, channelMember bool) map[string]any
}

func New(a *app.App) *Module {
	m := &Module{
		a: a, st: &store{db: a.DB}, sessions: newSessions(),
		turns: map[string]*turn{}, attached: map[string]*turn{}, starting: map[string]bool{}, stopStarting: map[string]bool{},
		bgTasks: map[string][]backgroundTask{}, bgOwner: map[string]string{}, pending: map[string][]claude.Message{},
		live: map[string]*claude.Session{},
	}
	m.runTurn = m.startTurn
	a.Hooks.StartChatTurn = m.hookStartTurn
	a.Hooks.CancelChatTurn = func(chatID string) error { m.cancelTurn(chatID, 10*time.Second); return nil }
	a.Hooks.ChatTurnActive = m.busy
	a.Hooks.GenerateText = m.generateText
	a.Sandboxes.OnStop = func(sandboxID string) { m.sessions.terminateSandbox(sandboxID, m.busy) }
	a.Sandboxes.Working = func(sandboxID string) bool { return m.sessions.anyInSandbox(sandboxID, m.keepAlive) }
	a.Hooks.OnChatDeleted = append(a.Hooks.OnChatDeleted, func(chatID string) {
		go func() {
			m.cancelTurn(chatID, 10*time.Second)
			m.sessions.terminate(chatID)
		}()
	})
	return m
}

func (m *Module) Start(ctx context.Context) {
	m.reconcileOrphans(ctx)
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.sessions.closeIdle(m.a.Cfg.ChatProcessIdleTTL, m.keepAlive)
		}
	}
}

func (m *Module) Stop(ctx context.Context) {
	m.mu.Lock()
	turns := make([]*turn, 0, len(m.turns))
	for _, t := range m.turns {
		turns = append(turns, t)
	}
	m.mu.Unlock()
	for _, t := range turns {
		t.cancelled.Store(true)
		t.cancel()
	}
	for _, t := range turns {
		select {
		case <-t.done:
		case <-ctx.Done():
		}
	}
	m.sessions.closeAll()
}

// reconcileOrphans finalizes messages a previous process left in progress.
func (m *Module) reconcileOrphans(ctx context.Context) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT id FROM messages WHERE stream_status = 'in_progress' AND deleted_at IS NULL`)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		m.finalizeDead(ctx, id, StatusInterrupted, "", "")
	}
	if len(ids) > 0 {
		slog.Info("reconciled orphaned in-progress messages", "count", len(ids))
	}
}

// finalizeDead folds a dead stream's logged events into its snapshot and
// writes a terminal event so reconnecting clients settle.
func (m *Module) finalizeDead(ctx context.Context, messageID, status, errText, publishUser string) {
	msg, err := m.st.getMessage(ctx, messageID)
	if err != nil || msg.StreamStatus != StatusInProgress {
		return
	}
	t := &turn{m: m, req: &turnRequest{messageID: messageID, chat: &models.Chat{ID: msg.ChatID}}, chatID: msg.ChatID}
	if events, ok := msg.ContentRender["events"].([]any); ok {
		for _, e := range events {
			if ev, ok := e.(map[string]any); ok {
				t.render = append(t.render, ev)
				if ev["type"] == "assistant_text" {
					if s, _ := ev["text"].(string); s != "" {
						t.textParts = append(t.textParts, s)
					}
				}
			}
		}
	}
	t.lastSeq = msg.LastSeq
	for {
		page, err := m.st.messageEventsAfter(ctx, messageID, t.lastSeq, 500)
		if err != nil {
			break
		}
		for _, e := range page {
			if snapshotKinds[e.Kind] {
				t.applySnapshot(e.Kind, e.Payload.V)
			}
			t.lastSeq = e.Seq
		}
		if len(page) < 500 {
			break
		}
	}
	if errText != "" {
		t.applySnapshot("assistant_text", map[string]any{"text": "\n\nError: " + errText})
	}
	content := t.contentText()
	if content == "" && errText != "" {
		content = errText
	}
	dur, _ := m.st.updateSnapshot(ctx, messageID, snapshot{ContentText: content, Render: t.renderCopy(), LastSeq: t.lastSeq, Status: status, RecordActivity: publishUser != ""})
	kind, payload := "cancelled", map[string]any{"status": status, "duration_ms": dur}
	if status != StatusInterrupted {
		kind, payload = "error", map[string]any{"error": firstNonEmpty(errText, "Stream ended unexpectedly")}
	}
	streamID := db.Deref(msg.ActiveStreamID)
	if streamID == "" {
		streamID = uuid.NewString()
	}
	seq, err := m.st.appendEvents(ctx, msg.ChatID, messageID, streamID, []string{kind}, []map[string]any{payload}, false)
	if err == nil && publishUser != "" {
		m.publishEnvelope(publishUser, msg.ChatID, messageID, streamID, seq, kind, payload)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (m *Module) busy(chatID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turns[chatID] != nil || m.starting[chatID]
}

// keepAlive reports a session that must not be idle-closed: a turn is running
// or Claude still has background tasks (shells, subagents) going.
func (m *Module) keepAlive(chatID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turns[chatID] != nil || m.starting[chatID] || len(m.bgTasks[chatID]) > 0
}

func (m *Module) activeTurn(chatID string) *turn {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.turns[chatID]
}

func (m *Module) reserve(chatID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.turns[chatID] != nil || m.starting[chatID] {
		return false
	}
	m.starting[chatID] = true
	return true
}

func (m *Module) unreserve(chatID string) {
	m.mu.Lock()
	delete(m.starting, chatID)
	delete(m.stopStarting, chatID)
	m.mu.Unlock()
}

// launch registers t as the chat's running turn and runs it.
func (m *Module) launch(t *turn) {
	m.mu.Lock()
	m.turns[t.chatID] = t
	delete(m.starting, t.chatID)
	if m.stopStarting[t.chatID] {
		delete(m.stopStarting, t.chatID)
		t.cancelled.Store(true)
		t.cancel()
	}
	m.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("turn panicked", "chat", t.chatID, "panic", r)
			}
			m.release(t)
		}()
		t.run()
	}()
}

// release lets go of the chat once t has finished, then starts whatever was
// waiting for it.
func (m *Module) release(t *turn) {
	m.mu.Lock()
	released := m.turns[t.chatID] == t
	if released {
		delete(m.turns, t.chatID)
	}
	m.mu.Unlock()
	// Output Claude produced on its own after the turn ended (a
	// background task finished) waits in pending; stream it now.
	if m.hasPendingCycle(t.chatID) {
		m.startFollowup(t.chatID)
	} else if released && t.queueMode != "" {
		// A message queued after finish checked the queue saw the chat
		// busy and left it to this turn.
		_ = m.runQueued(t.chatID, t.req.userID, t.queueMode == queueSendNow)
	}
}

func (m *Module) attach(chatID string, t *turn) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	m.mu.Lock()
	m.attached[chatID] = t
	m.mu.Unlock()
	if held := m.pending[chatID]; len(held) > 0 {
		t.in.push(held...)
	}
	delete(m.pending, chatID)
}

// maxPending bounds output held for a turn that never attaches.
const maxPending = 20000

// detach stops routing to t; output it did not consume goes back to pending
// for the next turn (or a follow-up started when t's launch unwinds).
func (m *Module) detach(chatID string, t *turn) {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	m.mu.Lock()
	if m.attached[chatID] == t {
		delete(m.attached, chatID)
	}
	m.mu.Unlock()
	left := t.in.take()
	if t.sess == nil || t.sess.Closed() {
		return
	}
	if len(left) > 0 && (len(m.pending[chatID]) > 0 || hasCycleStart(left)) {
		m.pending[chatID] = append(left, m.pending[chatID]...)
	}
}

// hasCycleStart reports main-thread output, which means Claude Code is
// running a cycle. A background subagent's own messages don't count.
func hasCycleStart(msgs []claude.Message) bool {
	for _, msg := range msgs {
		if (msg.Type == "stream_event" || msg.Type == "assistant") && (msg.ParentToolUseID == nil || *msg.ParentToolUseID == "") {
			return true
		}
	}
	return false
}

func (m *Module) hasPendingCycle(chatID string) bool {
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	return len(m.pending[chatID]) > 0
}

func (m *Module) wireSession(s *claude.Session) {
	chatID := s.ChatID
	// A new process starts without background tasks; any listed belonged to
	// the one it replaces and would keep the chat (and box) awake forever.
	m.mu.Lock()
	m.live[chatID] = s
	had := len(m.bgTasks[chatID]) > 0
	delete(m.bgTasks, chatID)
	m.mu.Unlock()
	if had {
		m.publishBackgroundTasks(chatID)
	}
	go func() {
		<-s.Done()
		m.sessionClosed(s)
	}()
	s.Attach(func(msg claude.Message) { m.sessionOutput(s, msg) },
		func(id string, raw json.RawMessage) {
			m.mu.Lock()
			t := m.attached[chatID]
			m.mu.Unlock()
			if t == nil {
				if string(raw) != string(claude.CancelControl) {
					s.RespondControl(id, map[string]any{"behavior": "deny", "message": "No active turn"})
				}
				return
			}
			t.handleControl(id, raw)
		})
}

func (m *Module) isLive(s *claude.Session) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live[s.ChatID] == s
}

// sessionOutput routes s's output, dropping what a replaced session still
// prints while it shuts down.
func (m *Module) sessionOutput(s *claude.Session, msg claude.Message) {
	if m.isLive(s) {
		m.onSessionMessage(s.ChatID, msg)
	}
}

// sessionClosed clears the chat's per-process state when its live session
// exits. A replaced session's exit leaves its successor's state alone.
func (m *Module) sessionClosed(s *claude.Session) {
	chatID := s.ChatID
	m.mu.Lock()
	if m.live[chatID] != s {
		m.mu.Unlock()
		return
	}
	delete(m.live, chatID)
	// Its background tasks died with it.
	had := len(m.bgTasks[chatID]) > 0
	delete(m.bgTasks, chatID)
	m.mu.Unlock()
	m.dropPending(chatID)
	if had {
		m.publishBackgroundTasks(chatID)
	}
}

func (m *Module) onSessionMessage(chatID string, msg claude.Message) {
	if msg.Type == "system" {
		switch msg.Subtype {
		case "background_tasks_changed":
			m.updateBackgroundTasks(chatID, msg.Raw)
		case "task_started":
			m.noteTaskToolID(chatID, msg.Raw)
		}
	}
	m.routeMu.Lock()
	defer m.routeMu.Unlock()
	m.mu.Lock()
	t := m.attached[chatID]
	m.mu.Unlock()
	if t != nil {
		t.in.push(msg)
		return
	}
	if held, ok := m.pending[chatID]; ok {
		if len(held) < maxPending {
			m.pending[chatID] = append(held, msg)
		}
		return
	}
	// Main-thread output with no turn attached: Claude Code started a cycle
	// on its own (a background task finished). Hold it and stream it as a
	// follow-up message.
	if hasCycleStart([]claude.Message{msg}) {
		m.pending[chatID] = []claude.Message{msg}
		go m.startFollowup(chatID)
	}
}

func (m *Module) dropPending(chatID string) {
	m.routeMu.Lock()
	delete(m.pending, chatID)
	m.routeMu.Unlock()
}

func (m *Module) updateBackgroundTasks(chatID string, raw json.RawMessage) {
	m.mu.Lock()
	tasks := parseBackgroundTasks(raw, m.bgTasks[chatID])
	if len(tasks) == 0 {
		delete(m.bgTasks, chatID)
	} else {
		m.bgTasks[chatID] = tasks
	}
	m.mu.Unlock()
	m.publishBackgroundTasks(chatID)
}

// noteTaskToolID links a background task to the tool call that started it;
// background_tasks_changed doesn't carry it, task_started (sent right after) does.
func (m *Module) noteTaskToolID(chatID string, raw json.RawMessage) {
	var msg struct {
		TaskID    string `json:"task_id"`
		ToolUseID string `json:"tool_use_id"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.TaskID == "" || msg.ToolUseID == "" {
		return
	}
	m.mu.Lock()
	changed := false
	for i := range m.bgTasks[chatID] {
		if t := &m.bgTasks[chatID][i]; t.ID == msg.TaskID && t.ToolID == "" {
			t.ToolID, changed = msg.ToolUseID, true
		}
	}
	m.mu.Unlock()
	if changed {
		m.publishBackgroundTasks(chatID)
	}
}

func (m *Module) publishBackgroundTasks(chatID string) {
	m.mu.Lock()
	tasks := append([]backgroundTask{}, m.bgTasks[chatID]...)
	user := m.bgOwner[chatID]
	m.mu.Unlock()
	if user != "" {
		m.publishChatEvent(user, map[string]any{"kind": "background_tasks", "chat_id": chatID, "tasks": tasks})
	}
}

func (m *Module) persistSessionID(chatID, sessionID string) {
	_, _ = m.a.DB.ExecContext(context.Background(), `UPDATE chats SET session_id = ? WHERE id = ? AND (session_id IS NULL OR session_id != ?)`, sessionID, chatID, sessionID)
}

// cancelTurn interrupts the chat's turn and waits for it to unwind. A turn
// still starting is marked so it never runs the agent.
func (m *Module) cancelTurn(chatID string, timeout time.Duration) {
	m.mu.Lock()
	t := m.turns[chatID]
	if t == nil && m.starting[chatID] {
		m.stopStarting[chatID] = true
	}
	m.mu.Unlock()
	if t == nil {
		return
	}
	t.cancelled.Store(true)
	t.cancel()
	select {
	case <-t.done:
	case <-time.After(timeout):
	}
}

// sessionEnv is the per-session env. It includes the profile env vars so the
// session fingerprint changes (and the CLI restarts) when they are edited.
func (m *Module) sessionEnv(req *turnRequest) map[string]string {
	env := m.a.Store.UserEnv(context.Background(), req.userID)
	env["AGENTROVE_CHAT_ID"] = req.chat.ID
	return env
}

// sessionConfig builds the CLI launch config for a turn.
func (m *Module) sessionConfig(sb *sandbox.Sandbox, req *turnRequest) claude.Config {
	cfg := claude.Config{
		Model:          req.modelID,
		PermissionMode: req.permissionMode,
		Effort:         req.effort,
		Cwd:            sb.Resolve(db.Deref(req.chat.WorktreeCwd)),
		SystemPrompt:   req.systemPrompt,
		FullReplace:    req.fullReplace,
		ResumeID:       db.Deref(req.chat.SessionID),
		Settings:       map[string]any{"outputStyle": "Concise"},
		Env:            m.sessionEnv(req),
		Binary:         m.a.Cfg.ClaudeBinary,
	}
	st, err := m.a.Store.GetUserSettings(context.Background(), req.userID)
	if err == nil && st.AutoCompactWindow > 0 {
		cfg.Settings["autoCompactWindow"] = st.AutoCompactWindow
	}
	if sb.Kind == sandbox.KindDocker {
		cfg.Binary = "claude"
		if m.a.Cfg.ArtifactsEnabled {
			claudeDir := filepath.Join(m.a.Cfg.AgentHomeDir(req.userID), "claude")
			if err := artifact.InstallSkill(claudeDir, m.a.Cfg.FrontendURL); err != nil {
				slog.Warn("install artifacts skill", "user", req.userID, "err", err)
			}
		}
		if !req.fullReplace {
			cfg.SystemPrompt += boxPrompt
		}
	}
	var allow []string
	if m.MCPConfig != nil {
		cfg.MCPServers = m.MCPConfig(req.userID, req.chat.ID, sb.Kind, req.member != nil)
		if len(cfg.MCPServers) > 0 {
			// Publishing a (private) artifact never needs a permission prompt.
			allow = append(allow, artifactTools...)
		}
	}
	if sb.Kind == sandbox.KindDocker && m.a.Cfg.BrowserEnabled && (err != nil || st.BrowserToolsEnabled) {
		// MCPConfig's map is cached and shared: copy before adding to it.
		servers := maps.Clone(cfg.MCPServers)
		if servers == nil {
			servers = map[string]any{}
		}
		servers[browserServer] = browserMCPConfig(m.a.Cfg.BrowserIdleTTL)
		cfg.MCPServers = servers
		allow = append(allow, browserTools...)
		if !req.fullReplace {
			cfg.SystemPrompt += browserPrompt
		}
	}
	if len(allow) > 0 {
		cfg.Settings["permissions"] = map[string]any{"allow": allow}
	}
	return cfg
}

// browserServer is the MCP server name of the box's browser tools.
const browserServer = "browser"

// browserMCPConfig runs Playwright MCP against the box's shared browser
// through agentrove-browser-mcp (sandbox/docker), which starts the browser
// and playwright-mcp only once a tool is called.
func browserMCPConfig(idle time.Duration) map[string]any {
	return map[string]any{
		"type":    "stdio",
		"command": "agentrove-browser-mcp",
		"args":    []string{},
		"env":     map[string]string{"AGENTROVE_BROWSER_IDLE_SECONDS": strconv.Itoa(int(idle.Seconds()))},
	}
}

// browserTools are pre-allowed like the artifact tools: they act on a
// browser inside the box, and agentrove-browser-mcp confines the files they
// read and write to /tmp/playwright-mcp (its only root), so none can write
// into the project without a prompt. browser_run_code_unsafe is left out on
// purpose: it runs arbitrary JavaScript in the box's Node process, so it is
// as powerful as Bash and gets the same permission prompt. The list follows
// the tools of the pinned @playwright/mcp (--caps vision); a renamed tool
// just prompts.
var browserTools = func() []string {
	names := []string{"navigate", "navigate_back", "click", "hover", "drag", "drop", "type", "press_key",
		"fill_form", "select_option", "file_upload", "handle_dialog", "snapshot", "find", "take_screenshot",
		"evaluate", "console_messages", "network_requests", "network_request", "tabs", "wait_for", "resize",
		"emulate_media", "close", "mouse_move_xy", "mouse_click_xy", "mouse_drag_xy", "mouse_down", "mouse_up",
		"mouse_wheel"}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "mcp__" + browserServer + "__browser_" + n
	}
	return out
}()

const browserPrompt = "<browser>A headless Chromium runs in this box; use the mcp__browser__* tools to test web apps " +
	"(it starts on first use). Dev servers you run in the box are reachable from it at http://localhost:<port>. " +
	"Browser tools can only read and write files in /tmp/playwright-mcp: screenshots and saved output land there " +
	"(copy them elsewhere with Bash if needed), and a file to upload must be copied there first. " +
	"The user can watch and use the same browser in the Browser tab.</browser>\n"

var artifactTools = []string{"mcp__agentrove__create_artifact", "mcp__agentrove__update_artifact",
	"mcp__agentrove__edit_artifact", "mcp__agentrove__restore_artifact", "mcp__agentrove__get_artifact",
	"mcp__agentrove__list_artifacts", "mcp__agentrove__get_artifact_state", "mcp__agentrove__set_artifact_state"}

// boxPrompt covers what Claude can't discover itself about the user's box.
const boxPrompt = "\n<environment>Personal Ubuntu box with passwordless sudo (run `sudo apt-get update` before apt installs). " +
	"HOME persists across projects and restarts; apt packages don't. The box stops when idle, ending background processes.</environment>\n"

var imageMIME = map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp"}

// promptBlocks builds the user message: text plus inline images/PDFs; other
// files are copied into the workspace and referenced by path.
func (m *Module) promptBlocks(sb *sandbox.Sandbox, req *turnRequest) ([]map[string]any, error) {
	blocks := []map[string]any{{"type": "text", "text": req.prompt}}
	var notes []string
	for _, att := range req.attachments {
		rel := db.Deref(att.FilePath)
		if rel == "" {
			continue
		}
		full := filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			slog.Warn("attachment missing", "path", full)
			continue
		}
		name := firstNonEmpty(db.Deref(att.Filename), filepath.Base(rel))
		switch att.FileType {
		case "image":
			mt := imageMIME[strings.ToLower(filepath.Ext(name))]
			if mt == "" {
				mt = imageMIME[strings.ToLower(filepath.Ext(rel))]
			}
			if mt == "" {
				mt = "image/png"
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": mt, "data": base64.StdEncoding.EncodeToString(data)}})
		case "pdf":
			blocks = append(blocks, map[string]any{"type": "document", "source": map[string]any{"type": "base64", "media_type": "application/pdf", "data": base64.StdEncoding.EncodeToString(data)}})
		default:
			dest := filepath.Base(rel)
			// The workspace is box-writable: write through the swap-safe root
			// and never through a symlink planted at dest.
			if err := sb.WriteWorkspaceFile(dest, data); err != nil {
				return nil, fmt.Errorf("copy attachment into workspace: %w", err)
			}
			notes = append(notes, "- "+name+" is available at "+path.Join(sb.Root, dest))
		}
	}
	if len(notes) > 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": "<user_attachments>\nUser uploaded the following files. Read them from these sandbox paths when needed.\n" + strings.Join(notes, "\n") + "\n</user_attachments>"})
	}
	return blocks, nil
}

// prepareUserPrompt wraps the prompt with the user's custom instructions;
// slash commands pass through verbatim so Claude Code interprets them.
func prepareUserPrompt(prompt string, custom *string) string {
	if strings.HasPrefix(prompt, "/") {
		return prompt
	}
	var b strings.Builder
	if c := strings.TrimSpace(db.Deref(custom)); c != "" {
		b.WriteString("<user_instructions>\n" + c + "\n</user_instructions>\n\n")
	}
	b.WriteString("<user_prompt>" + prompt + "</user_prompt>")
	return b.String()
}

// systemPromptFor returns the session system prompt and whether it replaces
// Claude Code's own (custom personas do).
func systemPromptFor(st *models.UserSettings, persona string, member bool) (string, bool) {
	content := ""
	if persona != "" && persona != DefaultPersona && st.Personas.Valid {
		for _, p := range st.Personas.V {
			if p.Name == persona {
				content = "\n" + p.Content + "\n"
				break
			}
		}
	}
	if member {
		return content, false
	}
	return content + "\n" + prompts.ChatMentionsInstructions + "\n" + prompts.PromptSuggestionsInstructions, content != ""
}

// startInput is the normalized request for a new user turn.
type startInput struct {
	userID         string
	chatID         string
	prompt         string
	modelID        string
	permissionMode string
	thinkingMode   *string
	worktree       bool
	baseBranch     *string
	persona        string
	attachments    []models.Attachment
	member         func(string, map[string]any)
	onDone         func(string, string)
	unattended     bool
	includeChannel bool
	// reserved: the caller already holds the chat's reservation and releases
	// it itself if the turn does not launch.
	reserved bool
	// beforeLaunch runs once the turn's rows exist, before it starts streaming
	// (queued hand-offs announce the new message on the previous stream).
	beforeLaunch func(user, assistant *Message, checkpointID *string)
}

func validateTurn(in *startInput) (string, error) {
	if strings.TrimSpace(in.prompt) == "" {
		return "", httpx.Unprocessable("prompt must not be empty")
	}
	if len(in.prompt) > 100000 {
		return "", httpx.Unprocessable("prompt is too long")
	}
	if !models.ValidModelID(in.modelID) {
		return "", httpx.BadRequest("Unknown model: " + in.modelID)
	}
	if in.permissionMode == "" {
		in.permissionMode = "bypassPermissions"
	}
	if !models.ValidPermissionMode(in.permissionMode) {
		return "", httpx.Unprocessable("Invalid permission mode: " + in.permissionMode)
	}
	effort, ok := claude.Effort(in.thinkingMode)
	if !ok {
		return "", httpx.BadRequest(fmt.Sprintf("Effort `%s` isn't supported by this model. Supported: low, medium, high, xhigh, max.", db.Deref(in.thinkingMode)))
	}
	if in.baseBranch != nil && *in.baseBranch != "" && !gitops.ValidBaseRef(*in.baseBranch) {
		return "", httpx.BadRequest("Invalid base branch name")
	}
	if in.persona == "" {
		in.persona = DefaultPersona
	}
	return effort, nil
}

// startTurn creates the turn's rows and launches it. The chat must not have
// a running turn.
func (m *Module) startTurn(ctx context.Context, in startInput) (app.ChatTurnResult, error) {
	var res app.ChatTurnResult
	effort, err := validateTurn(&in)
	if err != nil {
		return res, err
	}
	if !in.reserved && !m.reserve(in.chatID) {
		return res, httpx.Conflict(errBusy.Error())
	}
	launched := false
	defer func() {
		if !launched && !in.reserved {
			m.unreserve(in.chatID)
			if in.member == nil {
				// A message queued while we held the chat was left to us.
				go func() { _ = m.runQueued(in.chatID, in.userID, false) }()
			}
		}
	}()

	var chat *models.Chat
	if in.includeChannel {
		chat, err = m.a.Store.GetMemberChat(ctx, in.userID, in.chatID)
	} else {
		chat, err = m.a.Store.GetChat(ctx, in.userID, in.chatID)
	}
	if err != nil {
		return res, err
	}
	ws, err := m.a.Store.GetWorkspaceByID(ctx, chat.WorkspaceID)
	if err != nil {
		return res, err
	}
	settings, err := m.a.Store.GetUserSettings(ctx, in.userID)
	if err != nil {
		return res, err
	}
	sb, err := m.a.Sandboxes.For(ws)
	if err != nil {
		return res, httpx.Errorf(503, "%v", err)
	}

	if err := m.bumpChatForTurn(ctx, chat, in); err != nil {
		return res, err
	}

	// A chat bound to a worktree (incl. a sub-thread inheriting its parent's)
	// always runs there; otherwise create one only when this turn asks.
	if chat.WorktreeCwd == nil && in.worktree {
		wt, err := gitops.CreateWorktree(ctx, sb, chat.ID, db.Deref(in.baseBranch))
		if err != nil {
			return res, httpx.BadRequest(err.Error())
		}
		chat.WorktreeCwd = &wt
		_, _ = m.a.DB.ExecContext(ctx, `UPDATE chats SET worktree_cwd = ? WHERE id = ?`, wt, chat.ID)
	}

	var userMsg, asstMsg *Message
	err = m.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		var err error
		if userMsg, err = m.st.createMessage(ctx, tx, chat.ID, "user", in.prompt, nil, in.attachments); err != nil {
			return err
		}
		model := in.modelID
		asstMsg, err = m.st.createMessage(ctx, tx, chat.ID, "assistant", "", &model, nil)
		return err
	})
	if err != nil {
		return res, err
	}
	var checkpointID *string
	if in.member == nil {
		checkpointID = m.createCheckpoint(ctx, sb, chat, asstMsg.ID)
	}

	system, full := systemPromptFor(settings, in.persona, in.member != nil)
	req := &turnRequest{
		chat: chat, ws: ws, userID: in.userID, prompt: prepareUserPrompt(in.prompt, settings.CustomInstructions),
		modelID: in.modelID, permissionMode: in.permissionMode, effort: effort, personaName: in.persona,
		systemPrompt: system, fullReplace: full, attachments: in.attachments, messageID: asstMsg.ID,
		isNewChat: chat.SessionID == nil, rawPrompt: in.prompt, member: in.member, onDone: in.onDone, unattended: in.unattended,
	}
	if in.beforeLaunch != nil {
		in.beforeLaunch(userMsg, asstMsg, checkpointID)
	}
	m.mu.Lock()
	m.bgOwner[chat.ID] = in.userID
	m.mu.Unlock()
	t := newTurn(m, req)
	if ctx.Err() != nil {
		// The client gave up on the start (Stop while the box woke up).
		t.cancelled.Store(true)
		t.cancel()
	}
	m.launch(t)
	launched = true
	if in.member == nil {
		m.publishChatEvent(in.userID, map[string]any{"kind": "stream_started", "chat_id": chat.ID, "message_id": asstMsg.ID})
	}
	res = app.ChatTurnResult{ChatID: chat.ID, MessageID: asstMsg.ID, LastSeq: chat.LastEventSeq, CheckpointID: checkpointID, WorktreeCwd: chat.WorktreeCwd}
	return res, nil
}

// bumpChatForTurn moves the chat (and its parent) to the top and records the
// turn's settings as the source of truth for follow-ups.
func (m *Module) bumpChatForTurn(ctx context.Context, chat *models.Chat, in startInput) error {
	now := db.Now()
	return m.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if chat.ParentChatID != nil {
			// Ordering-only bump: keep an already-read parent read.
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET last_viewed_at = ? WHERE id = ? AND last_viewed_at >= updated_at`, now, *chat.ParentChatID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET updated_at = ? WHERE id = ?`, now, *chat.ParentChatID); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET updated_at = ?, last_model_id = ?, last_thinking_mode = ?, last_persona_name = ? WHERE id = ?`,
			now, in.modelID, in.thinkingMode, in.persona, chat.ID)
		return err
	})
}

func (m *Module) createCheckpoint(ctx context.Context, sb *sandbox.Sandbox, chat *models.Chat, messageID string) *string {
	cwd := db.Deref(chat.WorktreeCwd)
	cp, err := gitops.CreateCheckpoint(ctx, sb, cwd)
	if err != nil || cp == nil {
		// No row: the turn shows no restore action.
		if err != nil {
			slog.Warn("checkpoint failed; restore unavailable for this turn", "chat", chat.ID, "err", err)
		}
		return nil
	}
	id := uuid.NewString()
	now := db.Now()
	_, err = m.a.DB.ExecContext(ctx, `INSERT INTO chat_checkpoints (id, chat_id, assistant_message_id, cwd, base_head, pre_run_diff, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, chat.ID, messageID, db.StrOrNil(cwd), cp.BaseHead, cp.PreRunDiff, now, now)
	if err != nil {
		return nil
	}
	m.pruneCheckpoints(ctx, chat.ID)
	return &id
}

// keepCheckpoints is how many recent turns per chat stay restorable; each
// checkpoint holds a whole dirty tree, so older ones are dropped.
const keepCheckpoints = 20

func (m *Module) pruneCheckpoints(ctx context.Context, chatID string) {
	_, err := m.a.DB.ExecContext(ctx, `DELETE FROM chat_checkpoints WHERE chat_id = ? AND id NOT IN
		(SELECT id FROM chat_checkpoints WHERE chat_id = ? ORDER BY created_at DESC, id DESC LIMIT ?)`, chatID, chatID, keepCheckpoints)
	if err != nil {
		slog.Warn("prune checkpoints", "chat", chatID, "err", err)
	}
}

const (
	queueAll     = "all"
	queueSendNow = "send_now"
)

func queuedInput(userID, chatID string, q *QueuedMessage) startInput {
	return startInput{
		userID: userID, chatID: chatID, prompt: q.Content, modelID: q.ModelID, permissionMode: q.PermissionMode,
		thinkingMode: q.ThinkingMode, worktree: q.Worktree, baseBranch: q.BaseBranch, persona: q.SelectedPersonaName, attachments: q.Attachments,
		reserved: true,
	}
}

// startQueued hands the stream over to the next queued message, if any.
func (m *Module) startQueued(prev *turn, sendNowOnly bool, priorDur *int64) bool {
	ctx := context.Background()
	q, err := m.st.popQueued(ctx, prev.chatID, sendNowOnly)
	if err != nil || q == nil {
		return false
	}
	// Swap prev for a reservation in one step, so nothing else can claim the
	// chat in between; the new turn replaces prev.
	m.mu.Lock()
	if m.turns[prev.chatID] != prev || m.starting[prev.chatID] {
		m.mu.Unlock()
		m.st.requeue(ctx, prev.chatID, q)
		return false
	}
	delete(m.turns, prev.chatID)
	m.starting[prev.chatID] = true
	m.mu.Unlock()
	in := queuedInput(prev.req.userID, prev.chatID, q)
	in.beforeLaunch = func(user, asst *Message, cp *string) {
		atts := []map[string]any(nil)
		if len(q.Attachments) > 0 {
			for _, a := range user.Attachments {
				atts = append(atts, map[string]any{"id": a.ID, "message_id": a.MessageID, "file_url": a.FileURL, "file_type": a.FileType, "filename": a.Filename, "created_at": a.CreatedAt})
			}
		}
		prev.emit("queue_processing", map[string]any{
			"queued_message_id": q.ID, "user_message_id": user.ID, "assistant_message_id": asst.ID, "checkpoint_id": cp,
			"content": q.Content, "model_id": q.ModelID, "attachments": atts, "prior_duration_ms": priorDur,
		})
	}
	if _, err := m.runTurn(ctx, in); err != nil {
		slog.Error("start queued message", "chat", prev.chatID, "err", err)
		m.st.requeue(ctx, prev.chatID, q)
		m.unreserve(prev.chatID)
		return false
	}
	return true
}

// enqueue adds a message to the chat's queue. The turn it was queued behind
// may have ended already, or be past its queue check, in which case it runs
// the queue as it lets go of the chat.
func (m *Module) enqueue(ctx context.Context, chatID, userID string, q *QueuedMessage) error {
	if err := m.st.enqueue(ctx, chatID, q); err != nil {
		return err
	}
	if !m.busy(chatID) {
		go func() { _ = m.runQueued(chatID, userID, false) }()
	}
	return nil
}

// runQueued starts the chat's next queued message unless something holds the
// chat. Every holder checks the queue when it lets go (launch, startTurn), and
// enqueueing checks busy after the row is written, so a queued message is
// either taken by the holder or started here, never left behind.
func (m *Module) runQueued(chatID, userID string, sendNowOnly bool) error {
	ctx := context.Background()
	for m.st.hasQueued(ctx, chatID, sendNowOnly) {
		if !m.reserve(chatID) {
			return nil // the holder runs the queue when it releases the chat
		}
		q, err := m.st.popQueued(ctx, chatID, sendNowOnly)
		if err != nil || q == nil {
			// Someone else took it; recheck for anything queued while we held the chat.
			m.unreserve(chatID)
			if err != nil {
				return err
			}
			continue
		}
		if _, err := m.runTurn(ctx, queuedInput(userID, chatID, q)); err != nil {
			// No retry: the same message would most likely fail again.
			slog.Error("start queued message", "chat", chatID, "err", err)
			m.st.requeue(ctx, chatID, q)
			m.unreserve(chatID)
			return err
		}
		return nil
	}
	return nil
}

// startFollowup streams a cycle Claude Code started on its own.
func (m *Module) startFollowup(chatID string) {
	// A running or starting turn takes the held output when it attaches, or
	// starts the follow-up when it unwinds.
	if !m.reserve(chatID) {
		return
	}
	fail := func() {
		m.unreserve(chatID)
		m.dropPending(chatID)
		if chat, err := m.a.Store.GetChatByID(context.Background(), chatID); err == nil {
			_ = m.runQueued(chatID, chat.UserID, false)
		}
	}
	ctx := context.Background()
	chat, err := m.a.Store.GetChatByID(ctx, chatID)
	if err != nil {
		fail()
		return
	}
	ws, err := m.a.Store.GetWorkspaceByID(ctx, chat.WorkspaceID)
	if err != nil {
		fail()
		return
	}
	model := firstNonEmpty(db.Deref(chat.LastModelID), "sonnet")
	asst, err := m.st.createMessage(ctx, m.a.DB, chatID, "assistant", "", &model, nil)
	if err != nil {
		fail()
		return
	}
	req := &turnRequest{chat: chat, ws: ws, userID: chat.UserID, modelID: model, messageID: asst.ID, followup: true}
	if s := m.sessions.get(chatID); s != nil {
		req.permissionMode = s.Mode()
		req.modelID = firstNonEmpty(s.Model(), model)
	}
	t := newTurn(m, req)
	// Reuse the live session as-is; a respawn would lose the cycle.
	m.launch(t)
	m.publishChatEvent(chat.UserID, map[string]any{"kind": "stream_started", "chat_id": chatID, "message_id": asst.ID})
}

// hookStartTurn serves automations, channels and the MCP server.
func (m *Module) hookStartTurn(r app.ChatTurnRequest) (app.ChatTurnResult, error) {
	atts := make([]models.Attachment, 0, len(r.Attachments))
	for _, a := range r.Attachments {
		atts = append(atts, models.Attachment{FileURL: a.FileURL, FilePath: a.FilePath, FileType: a.FileType, Filename: a.Filename})
	}
	return m.startTurn(context.Background(), startInput{
		userID: r.UserID, chatID: r.ChatID, prompt: r.Prompt, modelID: r.ModelID, permissionMode: r.PermissionMode,
		thinkingMode: r.ThinkingMode, worktree: r.Worktree, baseBranch: r.BaseBranch, persona: r.SelectedPersonaName,
		attachments: atts, member: r.MemberSink, onDone: r.OnDone, unattended: r.Unattended, includeChannel: r.MemberSink != nil,
	})
}

// oneShot returns a text-generation runner using the user's Claude login.
func (m *Module) oneShot(userID string) claude.OneShot {
	home := m.a.Cfg.AgentHomeDir(userID)
	// Profile env vars (e.g. ANTHROPIC_BASE_URL/AUTH_TOKEN) override the global env.
	env := m.a.Cfg.AgentGlobalEnv()
	for k, v := range m.a.Store.UserEnv(context.Background(), userID) {
		env[k] = v
	}
	return claude.OneShot{Binary: m.a.Cfg.ClaudeBinary, Env: env,
		ConfigDir: claude.ConfigDirFor(home), AsUID: sandbox.ContainerUID}
}

func (m *Module) generateText(ctx context.Context, userID, modelID, systemPrompt, prompt string) (string, error) {
	if modelID == "" {
		modelID = "haiku"
	}
	return m.oneShot(userID).Run(ctx, modelID, systemPrompt, prompt)
}

func (m *Module) generateTitle(userID string, chat *models.Chat, prompt string) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	st, err := m.a.Store.GetUserSettings(ctx, userID)
	if err != nil {
		return
	}
	title, err := m.generateText(ctx, userID, st.TitleModelID, prompts.GenerateTitleSystemPrompt,
		"Generate a title for this message:\n<message>\n"+prompt+"\n</message>")
	if err != nil {
		slog.Warn("title generation failed", "chat", chat.ID, "err", err)
		return
	}
	title = strings.Trim(strings.TrimSpace(title), `"'`)
	if title == "" {
		return
	}
	if len(title) > 255 {
		title = title[:255]
	}
	// updated_at untouched: a backfilled title isn't unseen activity.
	if _, err := m.a.DB.ExecContext(ctx, `UPDATE chats SET title = ? WHERE id = ?`, title, chat.ID); err != nil {
		return
	}
	m.publishChatEvent(userID, map[string]any{"kind": "title_updated", "chat_id": chat.ID, "title": title})
}

func guessMIME(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}
