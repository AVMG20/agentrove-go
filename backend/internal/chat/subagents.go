package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// Subagents back the session panel's Subagents card and the subagent pane.
// Every message of a chat's live session passes through the tracker, whether
// or not a turn is attached, so background subagents that outlive their turn
// are followed too. Only what's needed to see what a subagent is doing is
// kept: its prompt, a capped activity log (tool calls and short text, no
// thinking) and its outcome. Resuming a session doesn't need any of it.

const (
	subagentRunning   = "running"
	subagentCompleted = "completed"
	subagentFailed    = "failed"
	subagentStopped   = "stopped"

	maxSubagentActivity = 150
	maxSubagentText     = 500 // runes per text entry
	maxSubagentTitle    = 200
	maxSubagentResult   = 4000
	maxSubagentPrompt   = 20000
	keepSubagents       = 50 // per chat
	subagentFlushEvery  = 750 * time.Millisecond
	// A finished subagent stays in memory this long so a late task
	// notification can still correct an inferred outcome.
	subagentGrace = time.Minute
)

type subagentActivity struct {
	Kind   string  `json:"kind"` // text | tool
	ID     string  `json:"id,omitempty"`
	Name   string  `json:"name,omitempty"`
	Title  string  `json:"title,omitempty"`
	Text   string  `json:"text,omitempty"`
	Status string  `json:"status,omitempty"` // tool: started | completed | failed
	At     float64 `json:"at"`
}

// subagent is keyed by the id of the Agent tool call that spawned it.
type subagent struct {
	ID           string `json:"id"`
	ChatID       string `json:"chat_id"`
	Description  string `json:"description"`
	SubagentType string `json:"subagent_type"`
	Prompt       string `json:"prompt,omitempty"`
	Background   bool   `json:"background"`
	Status       string `json:"status"`
	Result       string `json:"result,omitempty"`
	ToolCount    int    `json:"tool_count"`
	LastActivity string `json:"last_activity"`
	// TaskID is the CLI's background task id, for stopping it; live only.
	TaskID     string             `json:"task_id,omitempty"`
	StartedAt  float64            `json:"started_at"`
	FinishedAt *float64           `json:"finished_at"`
	Activity   []subagentActivity `json:"activity,omitempty"`
}

func (s *subagent) summary() subagent {
	out := *s
	out.Prompt, out.Activity = "", nil
	return out
}

func (s *subagent) detail() subagent {
	out := *s
	out.Activity = append([]subagentActivity(nil), s.Activity...)
	if out.Activity == nil {
		out.Activity = []subagentActivity{}
	}
	return out
}

type liveSubagent struct {
	subagent
	dirty    bool
	inserted bool
	// listed: seen in the CLI's background task list, so leaving it means done.
	listed bool
}

type subagentTracker struct {
	mu    sync.Mutex
	chats map[string]map[string]*liveSubagent // chat -> spawning tool id
	// nested maps an Agent call made inside a subagent to its top-level
	// subagent, whose log its work joins.
	nested map[string]map[string]string
	now    func() time.Time
}

func newSubagentTracker() *subagentTracker {
	return &subagentTracker{chats: map[string]map[string]*liveSubagent{}, nested: map[string]map[string]string{}, now: time.Now}
}

type cliBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

func contentBlocks(raw json.RawMessage) []cliBlock {
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil {
		return nil
	}
	var blocks []cliBlock
	_ = json.Unmarshal(msg.Content, &blocks) // a plain-string content has no blocks
	return blocks
}

func isAgentTool(name string) bool { return name == "Agent" || name == "Task" }

func unixSeconds(t time.Time) float64 { return float64(t.UnixMilli()) / 1000 }

func fromUnixSeconds(s float64) time.Time { return time.UnixMilli(int64(s * 1000)) }

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return clipRunes(s, maxSubagentTitle)
}

func (r *subagentTracker) handle(chatID string, msg claude.Message) {
	if msg.IsReplay {
		return // history the CLI echoes back, not new work
	}
	switch msg.Type {
	case "assistant", "user":
	case "system":
		r.system(chatID, msg)
		return
	case "result":
		r.turnEnded(chatID)
		return
	default:
		return
	}
	blocks := contentBlocks(msg.Message)
	if len(blocks) == 0 {
		return
	}
	parent := db.Deref(msg.ParentToolUseID)
	now := unixSeconds(r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	if parent == "" {
		for _, b := range blocks {
			switch {
			case msg.Type == "assistant" && b.Type == "tool_use" && isAgentTool(b.Name):
				r.spawn(chatID, b, now)
			case msg.Type == "user" && b.Type == "tool_result":
				if s := r.chats[chatID][b.ToolUseID]; s != nil {
					r.returned(s, b, msg.ToolUseResult, now)
				}
			}
		}
		return
	}
	s := r.root(chatID, parent)
	if s == nil || s.Status != subagentRunning {
		return
	}
	for _, b := range blocks {
		switch {
		case b.Type == "text" && msg.Type == "assistant":
			text := strings.TrimSpace(b.Text)
			if text == "" {
				continue
			}
			s.add(subagentActivity{Kind: "text", Text: clipRunes(text, maxSubagentText), At: now})
			s.LastActivity = firstLine(text)
		case b.Type == "tool_use":
			var input map[string]any
			_ = json.Unmarshal(b.Input, &input)
			title := clipRunes(claude.ToolTitle(b.Name, input), maxSubagentTitle)
			s.ToolCount++
			s.add(subagentActivity{Kind: "tool", ID: b.ID, Name: b.Name, Title: title, Status: "started", At: now})
			s.LastActivity = title
			if isAgentTool(b.Name) {
				if r.nested[chatID] == nil {
					r.nested[chatID] = map[string]string{}
				}
				r.nested[chatID][b.ID] = s.ID
			}
		case b.Type == "tool_result":
			for i := len(s.Activity) - 1; i >= 0; i-- {
				if a := &s.Activity[i]; a.Kind == "tool" && a.ID == b.ToolUseID {
					a.Status = "completed"
					if b.IsError {
						a.Status = "failed"
					}
					break
				}
			}
		}
	}
	s.dirty = true
}

func (s *liveSubagent) add(a subagentActivity) {
	s.Activity = append(s.Activity, a)
	if over := len(s.Activity) - maxSubagentActivity; over > 0 {
		s.Activity = append(s.Activity[:0:0], s.Activity[over:]...)
	}
}

func (s *liveSubagent) lastText() string {
	for i := len(s.Activity) - 1; i >= 0; i-- {
		if s.Activity[i].Kind == "text" {
			return s.Activity[i].Text
		}
	}
	return ""
}

func (r *subagentTracker) root(chatID, parent string) *liveSubagent {
	if s := r.chats[chatID][parent]; s != nil {
		return s
	}
	if id, ok := r.nested[chatID][parent]; ok {
		return r.chats[chatID][id]
	}
	return nil
}

func (r *subagentTracker) spawn(chatID string, b cliBlock, now float64) {
	if r.chats[chatID] == nil {
		r.chats[chatID] = map[string]*liveSubagent{}
	}
	if r.chats[chatID][b.ID] != nil {
		return
	}
	var input map[string]any
	_ = json.Unmarshal(b.Input, &input)
	str := func(k string) string { s, _ := input[k].(string); return s }
	bg, _ := input["run_in_background"].(bool)
	r.chats[chatID][b.ID] = &liveSubagent{dirty: true, subagent: subagent{
		ID: b.ID, ChatID: chatID, Description: clipRunes(str("description"), maxSubagentTitle),
		SubagentType: firstNonEmpty(str("subagent_type"), "general-purpose"), Prompt: clipRunes(str("prompt"), maxSubagentPrompt),
		Background: bg, Status: subagentRunning, StartedAt: now,
	}}
}

// returned handles the spawning tool call's result: the subagent's answer,
// or for a background subagent only the news that it was launched.
func (r *subagentTracker) returned(s *liveSubagent, b cliBlock, toolUseResult json.RawMessage, now float64) {
	if s.Status != subagentRunning {
		return
	}
	var res struct {
		Status  string `json:"status"`
		AgentID string `json:"agentId"`
	}
	_ = json.Unmarshal(toolUseResult, &res)
	if !b.IsError && (res.Status == "async_launched" || s.Background) {
		s.Background = true
		if s.TaskID == "" {
			s.TaskID = res.AgentID
		}
		s.dirty = true
		return
	}
	status := subagentCompleted
	if b.IsError {
		status = subagentFailed
	}
	s.finish(status, claude.ResultText(b.Content), now)
}

func (s *liveSubagent) finish(status, result string, now float64) {
	if s.Status == subagentRunning {
		s.FinishedAt = &now
	}
	s.Status = status
	if result = strings.TrimSpace(result); result == "" {
		result = s.lastText()
	}
	if result != "" {
		s.Result = clipRunes(result, maxSubagentResult)
	}
	s.dirty = true
}

func (r *subagentTracker) system(chatID string, msg claude.Message) {
	var m struct {
		TaskID    string `json:"task_id"`
		ToolUseID string `json:"tool_use_id"`
		Status    string `json:"status"`
		Summary   string `json:"summary"`
	}
	if json.Unmarshal(msg.Raw, &m) != nil || m.TaskID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch msg.Subtype {
	case "task_started":
		if s := r.chats[chatID][m.ToolUseID]; s != nil && s.TaskID != m.TaskID {
			s.TaskID, s.dirty = m.TaskID, true
		}
	case "task_notification":
		for _, s := range r.chats[chatID] {
			if s.TaskID != m.TaskID {
				continue
			}
			status := m.Status
			if status != subagentFailed && status != subagentStopped {
				status = subagentCompleted
			}
			result := s.lastText()
			if s.Result != "" {
				result = s.Result
			}
			s.finish(status, firstNonEmpty(result, m.Summary), unixSeconds(r.now()))
		}
	}
}

// syncTasks follows the CLI's background task list: a background subagent
// that left it has finished (a task notification may still correct how).
func (r *subagentTracker) syncTasks(chatID string, tasks []backgroundTask) {
	now := unixSeconds(r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.chats[chatID] {
		var found *backgroundTask
		for i := range tasks {
			if t := &tasks[i]; t.ToolID == s.ID || (s.TaskID != "" && t.ID == s.TaskID) {
				found = t
				break
			}
		}
		if found != nil {
			if !s.listed || !s.Background || s.TaskID != found.ID {
				s.listed, s.Background, s.TaskID, s.dirty = true, true, found.ID, true
			}
			continue
		}
		if s.listed && s.Status == subagentRunning {
			s.finish(subagentCompleted, "", now)
		}
	}
}

// turnEnded: a foreground subagent can't outlive its turn, so one still
// running when the turn ends was cut off.
func (r *subagentTracker) turnEnded(chatID string) {
	now := unixSeconds(r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.chats[chatID] {
		if s.Status == subagentRunning && !s.Background && !s.listed {
			s.finish(subagentStopped, "", now)
		}
	}
}

// stopAll: the chat's process exited or was replaced, taking its subagents with it.
func (r *subagentTracker) stopAll(chatID string) {
	now := unixSeconds(r.now())
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.chats[chatID] {
		if s.Status == subagentRunning {
			s.finish(subagentStopped, "", now)
		}
	}
}

// takeDirty returns copies of the subagents changed since the last call and
// whether each still needs inserting, and forgets finished ones past grace.
func (r *subagentTracker) takeDirty() (out []subagent, inserts []bool) {
	cutoff := unixSeconds(r.now().Add(-subagentGrace))
	r.mu.Lock()
	defer r.mu.Unlock()
	for chatID, subs := range r.chats {
		for id, s := range subs {
			if s.dirty {
				out = append(out, s.detail())
				inserts = append(inserts, !s.inserted)
				s.dirty, s.inserted = false, true
				continue
			}
			if s.FinishedAt != nil && *s.FinishedAt < cutoff {
				delete(subs, id)
				for nid, root := range r.nested[chatID] {
					if root == id {
						delete(r.nested[chatID], nid)
					}
				}
			}
		}
		if len(subs) == 0 {
			delete(r.chats, chatID)
			delete(r.nested, chatID)
		}
	}
	return out, inserts
}

func (r *subagentTracker) live(chatID string) []subagent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]subagent, 0, len(r.chats[chatID]))
	for _, s := range r.chats[chatID] {
		out = append(out, s.detail())
	}
	return out
}

func (r *subagentTracker) get(chatID, id string) (subagent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.chats[chatID][id]; s != nil {
		return s.detail(), true
	}
	return subagent{}, false
}

// runSubagentFlush writes changed subagents and announces them, batching a
// chatty subagent's activity into one write and event per tick.
func (m *Module) runSubagentFlush(ctx context.Context) {
	t := time.NewTicker(subagentFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.flushSubagents(context.Background())
			return
		case <-t.C:
			m.flushSubagents(ctx)
		}
	}
}

func (m *Module) flushSubagents(ctx context.Context) {
	subs, inserts := m.subs.takeDirty()
	for i, s := range subs {
		if err := m.saveSubagent(ctx, s, inserts[i]); err != nil {
			slog.Warn("save subagent", "chat", s.ChatID, "err", err)
		}
		m.mu.Lock()
		user := m.bgOwner[s.ChatID]
		m.mu.Unlock()
		if user != "" {
			m.publishChatEvent(user, map[string]any{"kind": "subagent", "chat_id": s.ChatID, "subagent": s.summary()})
		}
	}
}

func (m *Module) saveSubagent(ctx context.Context, s subagent, insert bool) error {
	var finished db.NullTime
	if s.FinishedAt != nil {
		finished = db.NT(fromUnixSeconds(*s.FinishedAt))
	}
	_, err := m.st.db.ExecContext(ctx, `INSERT INTO chat_subagents (id, chat_id, description, subagent_type, prompt, background, status, result, tool_count, last_activity, activity, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chat_id, id) DO UPDATE SET background = excluded.background, status = excluded.status, result = excluded.result,
			tool_count = excluded.tool_count, last_activity = excluded.last_activity, activity = excluded.activity, finished_at = excluded.finished_at`,
		s.ID, s.ChatID, s.Description, s.SubagentType, s.Prompt, s.Background, s.Status, s.Result, s.ToolCount, s.LastActivity,
		db.J(s.Activity), db.T(fromUnixSeconds(s.StartedAt)), finished)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return nil // the chat was deleted meanwhile
		}
		return err
	}
	if insert {
		_, err = m.st.db.ExecContext(ctx, `DELETE FROM chat_subagents WHERE chat_id = ? AND id NOT IN
			(SELECT id FROM chat_subagents WHERE chat_id = ? ORDER BY started_at DESC, id DESC LIMIT ?)`, s.ChatID, s.ChatID, keepSubagents)
	}
	return err
}

// reconcileSubagents marks subagents a previous process left running: they
// died with it.
func (m *Module) reconcileSubagents(ctx context.Context) {
	_, err := m.st.db.ExecContext(ctx, `UPDATE chat_subagents SET status = ?, finished_at = ? WHERE status = ?`, subagentStopped, db.Now(), subagentRunning)
	if err != nil {
		slog.Warn("reconcile subagents", "err", err)
	}
}

const subagentColumns = `id, chat_id, description, subagent_type, background, status, result, tool_count, last_activity, started_at, finished_at`

func scanSubagent(row interface{ Scan(...any) error }, extra ...any) (subagent, error) {
	var s subagent
	var started db.Time
	var finished db.NullTime
	err := row.Scan(append([]any{&s.ID, &s.ChatID, &s.Description, &s.SubagentType, &s.Background, &s.Status, &s.Result,
		&s.ToolCount, &s.LastActivity, &started, &finished}, extra...)...)
	s.StartedAt = unixSeconds(started.Time)
	if finished.Valid {
		f := unixSeconds(finished.Time.Time)
		s.FinishedAt = &f
	}
	return s, err
}

// listSubagents returns the chat's subagents, newest first, without prompts
// or activity.
func (m *Module) listSubagents(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	rows, err := m.st.db.QueryContext(r.Context(), `SELECT `+subagentColumns+` FROM chat_subagents WHERE chat_id = ? ORDER BY started_at DESC, id DESC LIMIT ?`, c.ID, keepSubagents)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[string]subagent{}
	for rows.Next() {
		s, err := scanSubagent(rows)
		if err != nil {
			return err
		}
		byID[s.ID] = s
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Live state is fresher than the last flush.
	for _, s := range m.subs.live(c.ID) {
		byID[s.ID] = s.summary()
	}
	out := make([]subagent, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt != out[j].StartedAt {
			return out[i].StartedAt > out[j].StartedAt
		}
		return out[i].ID > out[j].ID
	})
	if len(out) > keepSubagents {
		out = out[:keepSubagents]
	}
	return httpx.OK(w, out)
}

// getSubagent returns one subagent with its prompt and activity log.
func (m *Module) getSubagent(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	id := r.PathValue("subagent_id")
	if s, ok := m.subs.get(c.ID, id); ok {
		return httpx.OK(w, s)
	}
	var prompt string
	var activity db.JSON[[]subagentActivity]
	s, err := scanSubagent(m.st.db.QueryRowContext(r.Context(), `SELECT `+subagentColumns+`, prompt, activity FROM chat_subagents WHERE chat_id = ? AND id = ?`, c.ID, id),
		&prompt, &activity)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NotFound("Subagent not found")
	}
	if err != nil {
		return err
	}
	s.Prompt, s.Activity = prompt, activity.V
	return httpx.OK(w, s.detail())
}
