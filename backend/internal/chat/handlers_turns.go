package chat

import (
	"net/http"
	"time"

	"agentrove/internal/httpx"
)

// --- turns ---

func (m *Module) sendMessage(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 64<<20); err != nil {
		return err
	}
	u := user(r)
	chatID := r.FormValue("chat_id")
	c, err := m.a.Store.GetChat(r.Context(), u.ID, chatID)
	if err != nil {
		return err
	}
	atts, err := m.saveUploads(r, u.ID)
	if err != nil {
		return err
	}
	persona := r.FormValue("selected_persona_name")
	res, err := m.startTurn(r.Context(), startInput{
		userID: u.ID, chatID: c.ID, prompt: r.FormValue("prompt"), modelID: r.FormValue("model_id"),
		permissionMode: r.FormValue("permission_mode"), thinkingMode: httpx.FormOptional(r, "thinking_mode"),
		worktree: httpx.FormBool(r, "worktree", false), baseBranch: httpx.FormOptional(r, "base_branch"),
		persona: persona, attachments: atts,
	})
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}

func (m *Module) cancelStream(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	m.cancelTurn(c.ID, 10*time.Second)
	return httpx.NoContent(w)
}

func (m *Module) streamStatus(w http.ResponseWriter, r *http.Request) error {
	inactive := map[string]any{"has_active_task": false, "stream_id": nil, "last_seq": 0, "pending_elicitations": []any{}}
	chatID := r.PathValue("chat_id")
	t := m.activeTurn(chatID)
	if t == nil {
		return httpx.OK(w, inactive)
	}
	if _, err := m.a.Store.GetChat(r.Context(), user(r).ID, chatID); err != nil {
		return err
	}
	msg, err := m.st.inProgressAssistant(r.Context(), chatID)
	if err != nil {
		return err
	}
	if msg == nil {
		return httpx.OK(w, inactive)
	}
	return httpx.OK(w, map[string]any{"has_active_task": true, "message_id": msg.ID, "stream_id": msg.ActiveStreamID,
		"last_seq": msg.LastSeq, "pending_elicitations": t.pendingElicitations()})
}

func (m *Module) activeStreams(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	m.mu.Lock()
	ids := make([]string, 0, len(m.turns))
	for id := range m.turns {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	out := []map[string]any{}
	for _, id := range ids {
		if _, err := m.a.Store.GetChat(r.Context(), u.ID, id); err != nil {
			continue
		}
		msg, err := m.st.inProgressAssistant(r.Context(), id)
		if err != nil || msg == nil {
			continue
		}
		out = append(out, map[string]any{"chat_id": id, "message_id": msg.ID, "stream_id": msg.ActiveStreamID, "last_seq": msg.LastSeq})
	}
	return httpx.OK(w, out)
}

func (m *Module) backgroundTasks(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []map[string]any{}
	for chatID, tasks := range m.bgTasks {
		if m.bgOwner[chatID] != u.ID || len(tasks) == 0 {
			continue
		}
		out = append(out, map[string]any{"chat_id": chatID, "tasks": tasks})
	}
	return httpx.OK(w, out)
}

func (m *Module) stopBackgroundTask(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	s := m.sessions.get(c.ID)
	if s == nil {
		return httpx.NotFound("No running background command with that id")
	}
	if err := s.StopTask(r.Context(), r.PathValue("task_id")); err != nil {
		return httpx.NotFound("No running background command with that id")
	}
	return httpx.NoContent(w)
}

func (m *Module) respondPermission(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 1<<20); err != nil {
		return err
	}
	c, err := m.a.Store.GetMemberChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	t := m.activeTurn(c.ID)
	if t == nil || !t.resolvePermission(r.PathValue("request_id"), r.FormValue("option_id")) {
		return httpx.NotFound("Permission request not found or expired")
	}
	return httpx.OK(w, map[string]bool{"success": true})
}

func (m *Module) respondElicitation(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	var body struct {
		RequestID string         `json:"request_id"`
		Action    string         `json:"action"`
		Content   map[string]any `json:"content"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.RequestID != r.PathValue("request_id") {
		return httpx.BadRequest("Elicitation request ID does not match path")
	}
	if body.Action != "accept" && body.Action != "decline" && body.Action != "cancel" {
		return httpx.Unprocessable("action must be accept, decline or cancel")
	}
	t := m.activeTurn(c.ID)
	if t == nil || !t.resolveElicitation(body.RequestID, body.Action, body.Content) {
		return httpx.NotFound("Elicitation request not found or expired")
	}
	return httpx.OK(w, map[string]bool{"success": true})
}
