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
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agentrove/internal/app"
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

	// MCPConfig returns the mcpServers map for a chat's Claude session. It
	// must be stable across turns: the session fingerprint includes it.
	MCPConfig func(userID, chatID, sandboxKind string, channelMember bool) map[string]any
}

func New(a *app.App) *Module {
	m := &Module{
		a: a, st: &store{db: a.DB}, sessions: newSessions(),
		turns: map[string]*turn{}, attached: map[string]*turn{}, starting: map[string]bool{},
		bgTasks: map[string][]backgroundTask{}, bgOwner: map[string]string{},
	}
	a.Hooks.StartChatTurn = m.hookStartTurn
	a.Hooks.CancelChatTurn = func(chatID string) error { m.cancelTurn(chatID, 10*time.Second); return nil }
	a.Hooks.ChatTurnActive = m.busy
	a.Hooks.GenerateText = m.generateText
	a.Sandboxes.OnStop = func(sandboxID string) { m.sessions.terminateSandbox(sandboxID, m.busy) }
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
			m.sessions.closeIdle(m.a.Cfg.ChatProcessIdleTTL, m.busy)
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
	m.mu.Unlock()
}

// launch registers t as the chat's running turn and runs it.
func (m *Module) launch(t *turn) {
	m.mu.Lock()
	m.turns[t.chatID] = t
	delete(m.starting, t.chatID)
	m.mu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("turn panicked", "chat", t.chatID, "panic", r)
			}
			m.mu.Lock()
			if m.turns[t.chatID] == t {
				delete(m.turns, t.chatID)
			}
			m.mu.Unlock()
		}()
		t.run()
	}()
}

func (m *Module) attach(chatID string, t *turn) {
	m.mu.Lock()
	m.attached[chatID] = t
	m.mu.Unlock()
}

func (m *Module) detach(chatID string, t *turn) {
	m.mu.Lock()
	if m.attached[chatID] == t {
		delete(m.attached, chatID)
	}
	m.mu.Unlock()
}

func (m *Module) wireSession(s *claude.Session) {
	chatID := s.ChatID
	s.Attach(func(msg claude.Message) { m.onSessionMessage(chatID, msg) },
		func(id string, raw json.RawMessage) {
			m.mu.Lock()
			t := m.attached[chatID]
			m.mu.Unlock()
			if t == nil {
				s.RespondControl(id, map[string]any{"behavior": "deny", "message": "No active turn"})
				return
			}
			t.handleControl(id, raw)
		})
}

func (m *Module) onSessionMessage(chatID string, msg claude.Message) {
	if msg.Type == "system" && msg.Subtype == "background_tasks_changed" {
		m.updateBackgroundTasks(chatID, msg.Raw)
	}
	m.mu.Lock()
	t := m.attached[chatID]
	m.mu.Unlock()
	if t != nil {
		t.msgs <- msg
		return
	}
	// Output with no turn attached: Claude Code started a cycle on its own
	// (a background task finished). Stream it as a follow-up message.
	if msg.Type == "stream_event" || msg.Type == "assistant" {
		go m.startFollowup(chatID, msg)
	}
}

func (m *Module) updateBackgroundTasks(chatID string, raw json.RawMessage) {
	m.mu.Lock()
	tasks := parseBackgroundTasks(raw, m.bgTasks[chatID])
	if len(tasks) == 0 {
		delete(m.bgTasks, chatID)
	} else {
		m.bgTasks[chatID] = tasks
	}
	user := m.bgOwner[chatID]
	m.mu.Unlock()
	if user != "" {
		m.publishChatEvent(user, map[string]any{"kind": "background_tasks", "chat_id": chatID, "tasks": tasks})
	}
}

func (m *Module) persistSessionID(chatID, sessionID string) {
	_, _ = m.a.DB.ExecContext(context.Background(), `UPDATE chats SET session_id = ? WHERE id = ? AND (session_id IS NULL OR session_id != ?)`, sessionID, chatID, sessionID)
}

// cancelTurn interrupts the chat's turn and waits for it to unwind.
func (m *Module) cancelTurn(chatID string, timeout time.Duration) {
	t := m.activeTurn(chatID)
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
		Env:            map[string]string{"AGENTROVE_CHAT_ID": req.chat.ID},
		Binary:         m.a.Cfg.ClaudeBinary,
	}
	if sb.Kind == sandbox.KindDocker {
		cfg.Binary = "claude"
	}
	if m.MCPConfig != nil {
		cfg.MCPServers = m.MCPConfig(req.userID, req.chat.ID, sb.Kind, req.member != nil)
	}
	return cfg
}

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
			host, err := sb.HostPath(sb.Resolve(dest))
			if err != nil {
				continue
			}
			if err := os.WriteFile(host, data, 0o644); err != nil {
				return nil, fmt.Errorf("copy attachment into workspace: %w", err)
			}
			if sb.Kind == sandbox.KindDocker && os.Geteuid() == 0 {
				_ = os.Chown(host, sandbox.ContainerUID, sandbox.ContainerUID)
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
	if !m.reserve(in.chatID) {
		return res, httpx.Conflict(errBusy.Error())
	}
	launched := false
	defer func() {
		if !launched {
			m.unreserve(in.chatID)
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
	m.launch(newTurn(m, req))
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
		if err != nil {
			slog.Warn("checkpoint failed", "chat", chat.ID, "err", err)
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
	return &id
}

// startQueued hands the stream over to the next queued message, if any.
func (m *Module) startQueued(prev *turn, sendNowOnly bool, priorDur *int64) bool {
	ctx := context.Background()
	q, err := m.st.popQueued(ctx, prev.chatID, sendNowOnly)
	if err != nil || q == nil {
		return false
	}
	// Release the chat so startTurn can claim it; the new turn replaces prev.
	m.mu.Lock()
	if m.turns[prev.chatID] == prev {
		delete(m.turns, prev.chatID)
	}
	m.mu.Unlock()
	in := startInput{
		userID: prev.req.userID, chatID: prev.chatID, prompt: q.Content, modelID: q.ModelID, permissionMode: q.PermissionMode,
		thinkingMode: q.ThinkingMode, worktree: q.Worktree, baseBranch: q.BaseBranch, persona: q.SelectedPersonaName, attachments: q.Attachments,
		beforeLaunch: func(user, asst *Message, cp *string) {
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
		},
	}
	if _, err := m.startTurn(ctx, in); err != nil {
		slog.Error("start queued message", "chat", prev.chatID, "err", err)
		m.st.requeue(ctx, prev.chatID, q)
		return false
	}
	return true
}

// startFollowup streams a cycle Claude Code started on its own.
func (m *Module) startFollowup(chatID string, first claude.Message) {
	if !m.reserve(chatID) {
		return
	}
	ctx := context.Background()
	chat, err := m.a.Store.GetChatByID(ctx, chatID)
	if err != nil {
		m.unreserve(chatID)
		return
	}
	ws, err := m.a.Store.GetWorkspaceByID(ctx, chat.WorkspaceID)
	if err != nil {
		m.unreserve(chatID)
		return
	}
	model := firstNonEmpty(db.Deref(chat.LastModelID), "sonnet")
	asst, err := m.st.createMessage(ctx, m.a.DB, chatID, "assistant", "", &model, nil)
	if err != nil {
		m.unreserve(chatID)
		return
	}
	req := &turnRequest{chat: chat, ws: ws, userID: chat.UserID, modelID: model, messageID: asst.ID, followup: &first}
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
	return claude.OneShot{Binary: m.a.Cfg.ClaudeBinary, Env: m.a.Cfg.AgentGlobalEnv(),
		HomeDir: home, ConfigDir: claude.ConfigDirFor(home), AsUID: sandbox.ContainerUID}
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
