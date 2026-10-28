package chat

import (
	"net/http"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

// --- queue ---

func (m *Module) queueMessage(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 64<<20); err != nil {
		return err
	}
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	content := r.FormValue("content")
	if content == "" || len(content) > 100000 {
		return httpx.Unprocessable("content must be 1-100000 characters")
	}
	modelID := r.FormValue("model_id")
	if !models.ValidModelFor(c.Provider, modelID) {
		return httpx.BadRequest("Unknown model: " + modelID)
	}
	atts, err := m.saveUploads(r, u.ID)
	if err != nil {
		return err
	}
	q := &QueuedMessage{
		ID: uuid.NewString(), Content: content, ModelID: modelID, PermissionMode: firstNonEmpty(r.FormValue("permission_mode"), "bypassPermissions"),
		ThinkingMode: httpx.FormOptional(r, "thinking_mode"), Worktree: httpx.FormBool(r, "worktree", false), BaseBranch: httpx.FormOptional(r, "base_branch"),
		SelectedPersonaName: firstNonEmpty(r.FormValue("selected_persona_name"), DefaultPersona), QueuedAt: db.Now(), Attachments: atts,
	}
	steered, err := m.enqueue(r.Context(), c.ID, u.ID, q)
	if err != nil {
		return err
	}
	return httpx.Created(w, map[string]any{"id": q.ID, "queued_at": q.QueuedAt, "steered": steered})
}

func (m *Module) getQueue(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	q, err := m.st.listQueue(r.Context(), c.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, q)
}

func (m *Module) updateQueued(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Content == "" || len(body.Content) > 100000 {
		return httpx.Unprocessable("content must be 1-100000 characters")
	}
	res, err := m.a.DB.ExecContext(r.Context(), `UPDATE queued_messages SET content = ? WHERE id = ? AND chat_id = ?`, body.Content, r.PathValue("message_id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("Queued message not found")
	}
	q, err := scanQueued(m.a.DB.QueryRowContext(r.Context(), `SELECT `+queueColumns+` FROM queued_messages WHERE id = ?`, r.PathValue("message_id")))
	if err != nil {
		return err
	}
	return httpx.OK(w, q)
}

func (m *Module) deleteQueued(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	res, err := m.a.DB.ExecContext(r.Context(), `DELETE FROM queued_messages WHERE id = ? AND chat_id = ?`, r.PathValue("message_id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("Queued message not found")
	}
	return httpx.NoContent(w)
}

func (m *Module) clearQueue(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	if _, err := m.a.DB.ExecContext(r.Context(), `DELETE FROM queued_messages WHERE chat_id = ?`, c.ID); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// sendNow runs a queued message now: a running Claude turn forces it in as a
// steer (see steer.go), any other running turn is interrupted and the message
// takes over its stream; an idle chat starts it directly. On a running
// Claude turn it also works on a message the turn already holds.
func (m *Module) sendNow(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	if t := m.activeTurn(c.ID); t != nil {
		ok, err := m.sendNowRunning(r.Context(), t, c.ID, r.PathValue("message_id"))
		if err != nil {
			return err
		}
		if !ok {
			return httpx.NotFound("Queued message not found")
		}
		return httpx.NoContent(w)
	}
	res, err := m.a.DB.ExecContext(r.Context(), `UPDATE queued_messages SET send_now = 1 WHERE id = ? AND chat_id = ?`, r.PathValue("message_id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("Queued message not found")
	}
	if err := m.runQueued(c.ID, u.ID, true); err != nil {
		return httpx.Errorf(503, "Failed to start send-now execution")
	}
	return httpx.NoContent(w)
}
