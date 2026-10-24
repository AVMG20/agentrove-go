package chat

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
)

// --- messages ---

func (m *Module) listMessages(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return &httpx.Error{Status: http.StatusForbidden, Detail: "Chat not found or you don't have permission to access messages"}
	}
	limit := httpx.QueryInt(r, "limit", 20, 1, 100)
	out, err := m.st.listMessages(r.Context(), c.ID, r.URL.Query().Get("cursor"), limit, r.URL.Query().Get("omit_tool_results") == "true")
	if err != nil {
		return err
	}
	return httpx.OK(w, out)
}

func (m *Module) ownedMessage(r *http.Request) (*Message, error) {
	msg, err := m.st.getMessage(r.Context(), r.PathValue("message_id"))
	if err != nil {
		return nil, err
	}
	if _, err := m.a.Store.GetChat(r.Context(), user(r).ID, msg.ChatID); err != nil {
		return nil, httpx.NotFound("Chat not found or access denied")
	}
	return msg, nil
}

func (m *Module) messageEvents(w http.ResponseWriter, r *http.Request) error {
	msg, err := m.ownedMessage(r)
	if err != nil {
		return err
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)
	evs, err := m.st.messageEventsAfter(r.Context(), msg.ID, after, 5000)
	if err != nil {
		return err
	}
	out := make([]map[string]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, map[string]any{"id": fmt.Sprintf("%s-%d", e.StreamID, e.Seq), "message_id": e.MessageID, "chat_id": e.ChatID,
			"stream_id": e.StreamID, "seq": e.Seq, "event_type": e.Kind, "render_payload": e.Payload.V, "audit_payload": nil, "created_at": e.CreatedAt})
	}
	return httpx.OK(w, out)
}

func (m *Module) messageRender(w http.ResponseWriter, r *http.Request) error {
	msg, err := m.ownedMessage(r)
	if err != nil {
		return err
	}
	return httpx.OK(w, msg.ContentRender)
}

func (m *Module) restoreCheckpoint(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	var chatID, baseHead, diff string
	var cwd *string
	err := m.a.DB.QueryRowContext(r.Context(), `SELECT cp.chat_id, cp.cwd, cp.base_head, cp.pre_run_diff FROM chat_checkpoints cp
		JOIN chats c ON c.id = cp.chat_id WHERE cp.assistant_message_id = ? AND c.user_id = ? AND c.deleted_at IS NULL`,
		r.PathValue("message_id"), u.ID).Scan(&chatID, &cwd, &baseHead, &diff)
	if errors.Is(err, sql.ErrNoRows) {
		return httpx.NotFound("Checkpoint not found")
	}
	if err != nil {
		return err
	}
	c, err := m.a.Store.GetChat(r.Context(), u.ID, chatID)
	if err != nil {
		return err
	}
	ws, err := m.a.Store.GetWorkspaceByID(r.Context(), c.WorkspaceID)
	if err != nil {
		return err
	}
	sb, err := m.a.Sandboxes.For(ws)
	if err != nil {
		return err
	}
	res, err := gitops.RestoreCheckpoint(r.Context(), sb, db.Deref(cwd), baseHead, diff)
	if err != nil {
		return httpx.BadRequest(err.Error())
	}
	out := map[string]any{"success": res.ExitCode == 0, "output": strings.TrimSpace(res.Output())}
	if res.ExitCode != 0 {
		out["error"] = firstNonEmpty(strings.TrimSpace(res.Output()), "Restore failed")
	}
	return httpx.OK(w, out)
}
