package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"agentrove/internal/auth"
	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/prompts"

	"github.com/google/uuid"
)

func (m *Module) Routes(mux *http.ServeMux) {
	p := m.a.Cfg.APIPrefix + "/chat"
	req := m.a.Auth.Require
	mux.HandleFunc("POST "+p+"/chats", req(m.createChat))
	mux.HandleFunc("POST "+p+"/chat", req(m.sendMessage))
	mux.HandleFunc("POST "+p+"/enhance-prompt", req(m.enhancePrompt))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/generate-title", req(m.generateTitleHandler))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/ask-code", req(m.askCode))
	mux.HandleFunc("GET "+p+"/chats", req(m.listChats))
	mux.HandleFunc("GET "+p+"/chats/search", req(m.searchChats))
	mux.HandleFunc("GET "+p+"/chats/active-streams", req(m.activeStreams))
	mux.HandleFunc("GET "+p+"/chats/background-tasks", req(m.backgroundTasks))
	mux.HandleFunc("GET "+p+"/chats/events", req(m.chatEventsFeed))
	mux.HandleFunc("GET "+p+"/chats/streams", req(m.streamsFeed))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/sub-threads", req(m.subThreads))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}", req(m.getChat))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/context-usage", req(m.contextUsage))
	mux.HandleFunc("PATCH "+p+"/chats/{chat_id}", req(m.updateChat))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/viewed", req(m.markViewed))
	mux.HandleFunc("DELETE "+p+"/chats/all", req(m.deleteAllChats))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}", req(m.deleteChat))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/messages", req(m.listMessages))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/status", req(m.streamStatus))
	mux.HandleFunc("GET "+p+"/messages/{message_id}/events", req(m.messageEvents))
	mux.HandleFunc("GET "+p+"/messages/{message_id}/render", req(m.messageRender))
	mux.HandleFunc("POST "+p+"/messages/{message_id}/checkpoint/restore-all", req(m.restoreCheckpoint))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/background-tasks/{task_id}/stop", req(m.stopBackgroundTask))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/stream", req(m.cancelStream))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/permissions/{request_id}/respond", req(m.respondPermission))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/elicitation/{request_id}/respond", req(m.respondElicitation))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/queue", req(m.queueMessage))
	mux.HandleFunc("GET "+p+"/chats/{chat_id}/queue", req(m.getQueue))
	mux.HandleFunc("PATCH "+p+"/chats/{chat_id}/queue/{message_id}", req(m.updateQueued))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/queue/{message_id}", req(m.deleteQueued))
	mux.HandleFunc("POST "+p+"/chats/{chat_id}/queue/{message_id}/send-now", req(m.sendNow))
	mux.HandleFunc("DELETE "+p+"/chats/{chat_id}/queue", req(m.clearQueue))

	models := m.a.Cfg.APIPrefix + "/models"
	mux.HandleFunc("GET "+models, req(m.listModels))
	mux.HandleFunc("GET "+models+"/", req(m.listModels))

	att := m.a.Cfg.APIPrefix + "/attachments"
	mux.HandleFunc("GET "+att+"/temp/preview", req(m.tempPreview))
	mux.HandleFunc("GET "+att+"/{attachment_id}/preview", req(m.attachmentPreview))
	mux.HandleFunc("GET "+att+"/{attachment_id}/download", req(m.attachmentDownload))
}

func user(r *http.Request) *models.User { return auth.UserFrom(r.Context()) }

func (m *Module) listModels(w http.ResponseWriter, r *http.Request) error {
	if k := r.URL.Query().Get("agent_kind"); k != "" && k != "claude" {
		return httpx.OK(w, []models.ModelInfo{})
	}
	return httpx.OK(w, models.ClaudeModels)
}

// --- chats ---

func (m *Module) createChat(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	var body struct {
		Title        string  `json:"title"`
		ModelID      string  `json:"model_id"`
		WorkspaceID  string  `json:"workspace_id"`
		ParentChatID *string `json:"parent_chat_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Title == "" || len(body.Title) > 255 {
		return httpx.Unprocessable("title must be 1-255 characters")
	}
	c, err := m.CreateChat(r.Context(), u.ID, body.Title, body.WorkspaceID, body.ParentChatID)
	if err != nil {
		return err
	}
	return httpx.Created(w, c)
}

// CreateChat inserts a chat (optionally a sub-thread) and announces it.
func (m *Module) CreateChat(ctx context.Context, userID, title, workspaceID string, parentID *string) (*models.Chat, error) {
	var worktree *string
	if parentID != nil && *parentID != "" {
		parent, err := m.a.Store.GetChat(ctx, userID, *parentID)
		if err != nil {
			return nil, httpx.NotFound("Parent chat not found")
		}
		if parent.ParentChatID != nil {
			return nil, httpx.BadRequest("Cannot create a sub-thread of a sub-thread")
		}
		workspaceID = parent.WorkspaceID
		// Sub-threads run in the parent's worktree so they see its changes.
		worktree = parent.WorktreeCwd
		now := db.Now()
		_, _ = m.a.DB.ExecContext(ctx, `UPDATE chats SET last_viewed_at = CASE WHEN last_viewed_at >= updated_at THEN ? ELSE last_viewed_at END, updated_at = ? WHERE id = ?`, now, now, parent.ID)
	} else {
		parentID = nil
	}
	ws, err := m.a.Store.GetWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	now := db.Now()
	_, err = m.a.DB.ExecContext(ctx, `INSERT INTO chats (id, title, user_id, workspace_id, parent_chat_id, worktree_cwd, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, title, userID, ws.ID, parentID, worktree, now, now)
	if err != nil {
		return nil, err
	}
	c, err := m.a.Store.GetChat(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	m.publishChatEvent(userID, map[string]any{"kind": "chat_created", "chat": c})
	return c, nil
}

func (m *Module) listChats(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	q := r.URL.Query()
	page := httpx.QueryInt(r, "page", 1, 1, 0)
	perPage := httpx.QueryInt(r, "per_page", 10, 1, 100)
	where := []string{"c.user_id = ?", "c.deleted_at IS NULL", "c.channel_id IS NULL"}
	args := []any{u.ID}
	if q.Get("include_sub_threads") != "true" {
		where = append(where, "c.parent_chat_id IS NULL")
	}
	if ws := q.Get("workspace_id"); ws != "" {
		where = append(where, "c.workspace_id = ?")
		args = append(args, ws)
	}
	if pinned := httpx.QueryBool(r, "pinned"); pinned != nil {
		if *pinned {
			where = append(where, "c.pinned_at IS NOT NULL")
		} else {
			where = append(where, "c.pinned_at IS NULL")
		}
	}
	cond := strings.Join(where, " AND ")
	var total int
	if err := m.a.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM chats c WHERE `+cond, args...).Scan(&total); err != nil {
		return err
	}
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT `+models.ChatColumns+`,
		(SELECT COUNT(1) FROM chats s WHERE s.parent_chat_id = c.id AND s.deleted_at IS NULL AND s.channel_id IS NULL)
		FROM chats c JOIN workspaces w ON w.id = c.workspace_id WHERE `+cond+`
		ORDER BY (c.pinned_at IS NULL), c.pinned_at DESC, c.updated_at DESC LIMIT ? OFFSET ?`,
		append(args, perPage, (page-1)*perPage)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []*models.Chat{}
	for rows.Next() {
		c := &models.Chat{}
		if err := rows.Scan(append(c.ScanFields(), &c.SubThreadCount)...); err != nil {
			return err
		}
		c.Finish()
		items = append(items, c)
	}
	return httpx.OK(w, models.NewPaginated(items, page, perPage, total))
}

func (m *Module) subThreads(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	parent, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT `+models.ChatColumns+` FROM chats c JOIN workspaces w ON w.id = c.workspace_id
		WHERE c.parent_chat_id = ? AND c.user_id = ? AND c.deleted_at IS NULL AND c.channel_id IS NULL ORDER BY c.updated_at DESC`, parent.ID, u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []*models.Chat{}
	for rows.Next() {
		c := &models.Chat{}
		if err := rows.Scan(c.ScanFields()...); err != nil {
			return err
		}
		c.Finish()
		out = append(out, c)
	}
	return httpx.OK(w, out)
}

func (m *Module) getChat(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetMemberChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	_ = m.a.DB.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM chats WHERE parent_chat_id = ? AND deleted_at IS NULL AND channel_id IS NULL`, c.ID).Scan(&c.SubThreadCount)
	return httpx.OK(w, c)
}

func (m *Module) contextUsage(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	window := c.ContextWindow
	if window == 0 {
		window = claude.ContextWindow(db.Deref(c.LastModelID))
	}
	pct := 0.0
	if window > 0 {
		pct = float64(c.ContextTokenUsage) / float64(window) * 100
		if pct > 100 {
			pct = 100
		}
	}
	return httpx.OK(w, map[string]any{"tokens_used": c.ContextTokenUsage, "context_window": window, "percentage": pct})
}

func (m *Module) updateChat(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	var body struct {
		Title  *string `json:"title"`
		Pinned *bool   `json:"pinned"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	now := db.Now()
	sets := []string{"updated_at = ?"}
	args := []any{now}
	if body.Title != nil {
		if *body.Title == "" || len(*body.Title) > 255 {
			return httpx.Unprocessable("title must be 1-255 characters")
		}
		sets = append(sets, "title = ?")
		args = append(args, *body.Title)
	}
	if body.Pinned != nil {
		sets = append(sets, "pinned_at = ?")
		if *body.Pinned {
			args = append(args, now)
		} else {
			args = append(args, nil)
		}
	}
	// Rename/pin isn't new content: keep an already-read chat read.
	if !c.Unread {
		sets = append(sets, "last_viewed_at = ?")
		args = append(args, now)
	}
	args = append(args, c.ID)
	if _, err := m.a.DB.ExecContext(r.Context(), `UPDATE chats SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
		return err
	}
	c, err = m.a.Store.GetChat(r.Context(), u.ID, c.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, c)
}

func (m *Module) markViewed(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	_, err = m.a.DB.ExecContext(r.Context(), `UPDATE chats SET last_viewed_at = ? WHERE id = ?`, db.Now(), c.ID)
	if err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) deleteChat(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	if err := m.DeleteChats(r.Context(), u.ID, []string{c.ID}); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (m *Module) deleteAllChats(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT id FROM chats WHERE user_id = ? AND deleted_at IS NULL AND channel_id IS NULL AND parent_chat_id IS NULL`, u.ID)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := m.DeleteChats(r.Context(), u.ID, ids); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

// DeleteChats soft-deletes chats and their sub-threads, stops their agents,
// removes worktrees they own, and tears down workspaces left without chats.
func (m *Module) DeleteChats(ctx context.Context, userID string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	type victim struct{ id, workspace, worktree string }
	var victims []victim
	workspaces := map[string]bool{}
	now := db.Now()
	err := m.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		args := make([]any, 0, len(ids)+1)
		for _, id := range ids {
			args = append(args, id)
		}
		args = append(args, userID)
		ph := db.Placeholders(len(ids))
		rows, err := tx.QueryContext(ctx, `SELECT id, workspace_id, COALESCE(worktree_cwd, '') FROM chats
			WHERE (id IN (`+ph+`) OR parent_chat_id IN (`+ph+`)) AND user_id = ? AND deleted_at IS NULL AND channel_id IS NULL`,
			append(append(append([]any{}, args[:len(ids)]...), args[:len(ids)]...), userID)...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var v victim
			if err := rows.Scan(&v.id, &v.workspace, &v.worktree); err != nil {
				rows.Close()
				return err
			}
			victims = append(victims, v)
			workspaces[v.workspace] = true
		}
		rows.Close()
		for _, v := range victims {
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET deleted_at = ?, updated_at = ? WHERE id = ?`, now, now, v.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE messages SET deleted_at = ? WHERE chat_id = ? AND deleted_at IS NULL`, now, v.id); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM queued_messages WHERE chat_id = ?`, v.id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, v := range victims {
		id := v.id
		go func() {
			m.cancelTurn(id, 10*time.Second)
			m.sessions.terminate(id)
		}()
		for _, fn := range m.a.Hooks.OnChatDeleted {
			fn(id)
		}
	}
	// Workspaces with no chats left are deleted along with their sandbox,
	// matching the original behaviour; otherwise only owned worktrees go.
	for wsID := range workspaces {
		var remaining int
		_ = m.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats WHERE workspace_id = ? AND deleted_at IS NULL`, wsID).Scan(&remaining)
		ws, err := m.a.Store.GetWorkspaceByID(ctx, wsID)
		if err != nil || ws.DeletedAt.Valid {
			continue
		}
		if remaining == 0 {
			_, _ = m.a.DB.ExecContext(ctx, `UPDATE workspaces SET deleted_at = ?, updated_at = ? WHERE id = ?`, now, now, wsID)
			go func() {
				_ = m.a.Sandboxes.Delete(context.Background(), ws.SandboxProvider, ws.SandboxID)
				for _, fn := range m.a.Hooks.OnWorkspaceDeleted {
					fn(wsID)
				}
			}()
			continue
		}
		sb, err := m.a.Sandboxes.For(ws)
		if err != nil {
			continue
		}
		for _, v := range victims {
			if v.workspace == wsID && v.worktree != "" && v.worktree == gitops.ChatWorktreePath(v.id) {
				wt := v.worktree
				go gitops.RemoveWorktree(context.Background(), sb, wt)
			}
		}
	}
	return nil
}

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
	return httpx.OK(w, map[string]any{"success": res.ExitCode == 0, "output": strings.TrimSpace(res.Output())})
}

// --- AI helpers ---

func (m *Module) enhancePrompt(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 1<<20); err != nil {
		return err
	}
	prompt := r.FormValue("prompt")
	out, err := m.generateText(r.Context(), user(r).ID, r.FormValue("model_id"), prompts.EnhancePrompt, "Enhance this prompt: "+prompt)
	if err != nil {
		return httpx.Errorf(502, "%v", err)
	}
	if out == "" {
		out = prompt
	}
	return httpx.OK(w, map[string]string{"enhanced_prompt": out})
}

func (m *Module) generateTitleHandler(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	var prompt string
	err = m.a.DB.QueryRowContext(r.Context(), `SELECT content_text FROM messages WHERE chat_id = ? AND role = 'user' AND deleted_at IS NULL
		AND TRIM(content_text) != '' ORDER BY created_at LIMIT 1`, c.ID).Scan(&prompt)
	if err != nil {
		return httpx.BadRequest("Chat has no messages to generate a title from")
	}
	st, err := m.a.Store.GetUserSettings(r.Context(), u.ID)
	if err != nil {
		return err
	}
	title, err := m.generateText(r.Context(), u.ID, st.TitleModelID, prompts.GenerateTitleSystemPrompt, "Generate a title for this message:\n<message>\n"+prompt+"\n</message>")
	title = strings.Trim(strings.TrimSpace(title), `"'`)
	if err != nil || title == "" {
		return httpx.Errorf(503, "Title generation failed")
	}
	if len(title) > 255 {
		title = title[:255]
	}
	return httpx.OK(w, map[string]string{"title": title})
}

func (m *Module) askCode(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	if _, err := m.a.Store.GetMemberChat(r.Context(), u.ID, r.PathValue("chat_id")); err != nil {
		return err
	}
	var body struct {
		Question  string  `json:"question"`
		Code      string  `json:"code"`
		FilePath  *string `json:"file_path"`
		Language  *string `json:"language"`
		StartLine *int    `json:"start_line"`
		EndLine   *int    `json:"end_line"`
		ModelID   string  `json:"model_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Question == "" || body.Code == "" {
		return httpx.Unprocessable("question and code are required")
	}
	header := "Selected text from the conversation:"
	if body.FilePath != nil && body.Language != nil && body.StartLine != nil && body.EndLine != nil {
		ref := strconv.Itoa(*body.StartLine)
		if *body.EndLine != *body.StartLine {
			ref += "-" + strconv.Itoa(*body.EndLine)
		}
		header = "File: " + *body.FilePath + " (lines " + ref + ", " + *body.Language + ")"
	}
	msg := header + "\n<selection>\n" + body.Code + "\n</selection>\n\n<question>\n" + body.Question + "\n</question>"
	answer, err := m.generateText(r.Context(), u.ID, body.ModelID, prompts.InlineChatSystemPrompt, msg)
	if err != nil {
		return httpx.Errorf(502, "%v", err)
	}
	if answer == "" {
		return httpx.Errorf(502, "AI returned an empty answer")
	}
	return httpx.OK(w, map[string]string{"answer": answer})
}

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
	if !models.ValidModelID(modelID) {
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
	if err := m.st.enqueue(r.Context(), c.ID, q); err != nil {
		return err
	}
	return httpx.Created(w, map[string]any{"id": q.ID, "queued_at": q.QueuedAt})
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

// sendNow flags a queued message to run next: a running turn is interrupted
// and the message takes over its stream; an idle chat starts it directly.
func (m *Module) sendNow(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	res, err := m.a.DB.ExecContext(r.Context(), `UPDATE queued_messages SET send_now = 1 WHERE id = ? AND chat_id = ?`, r.PathValue("message_id"), c.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return httpx.NotFound("Queued message not found")
	}
	if t := m.activeTurn(c.ID); t != nil {
		t.cancelled.Store(true)
		t.cancel()
		return httpx.NoContent(w)
	}
	q, err := m.st.popQueued(r.Context(), c.ID, true)
	if err != nil || q == nil {
		return httpx.NoContent(w)
	}
	_, err = m.startTurn(r.Context(), startInput{
		userID: u.ID, chatID: c.ID, prompt: q.Content, modelID: q.ModelID, permissionMode: q.PermissionMode,
		thinkingMode: q.ThinkingMode, worktree: q.Worktree, baseBranch: q.BaseBranch, persona: q.SelectedPersonaName, attachments: q.Attachments,
	})
	if err != nil {
		m.st.requeue(context.Background(), c.ID, q)
		return httpx.Errorf(503, "Failed to start send-now execution")
	}
	return httpx.NoContent(w)
}

// --- attachments ---

var uploadFolders = map[string][2]string{
	"application/pdf": {"pdfs", "pdf"},
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": {"xlsx", "xlsx"},
}

var allowedImages = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

func (m *Module) saveUploads(r *http.Request, userID string) ([]models.Attachment, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File["attached_files"]
	out := make([]models.Attachment, 0, len(files))
	for _, fh := range files {
		a, err := m.saveUpload(fh, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (m *Module) saveUpload(fh *multipart.FileHeader, userID string) (models.Attachment, error) {
	ct := fh.Header.Get("Content-Type")
	var folder, kind string
	switch {
	case allowedImages[ct]:
		folder, kind = "images", "image"
	case uploadFolders[ct] != [2]string{}:
		folder, kind = uploadFolders[ct][0], uploadFolders[ct][1]
	default:
		return models.Attachment{}, httpx.BadRequest("Invalid file type: " + ct)
	}
	if fh.Size > m.a.Cfg.MaxUploadSize {
		return models.Attachment{}, httpx.BadRequest(fmt.Sprintf("File too large: %d bytes exceeds %d bytes", fh.Size, m.a.Cfg.MaxUploadSize))
	}
	src, err := fh.Open()
	if err != nil {
		return models.Attachment{}, err
	}
	defer src.Close()
	rel := filepath.ToSlash(filepath.Join("temp", userID, folder, uuid.NewString()+strings.ToLower(filepath.Ext(fh.Filename))))
	full := filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return models.Attachment{}, err
	}
	dst, err := os.Create(full)
	if err != nil {
		return models.Attachment{}, err
	}
	if _, err := io.Copy(dst, io.LimitReader(src, m.a.Cfg.MaxUploadSize+1)); err != nil {
		dst.Close()
		return models.Attachment{}, err
	}
	dst.Close()
	name := fh.Filename
	return models.Attachment{FileURL: m.a.Cfg.APIPrefix + "/attachments/temp/preview?path=" + rel, FilePath: &rel, FileType: kind, Filename: &name}, nil
}

func (m *Module) tempPreview(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	rel := filepath.Clean(filepath.FromSlash(r.URL.Query().Get("path")))
	prefix := filepath.Join("temp", u.ID) + string(filepath.Separator)
	if !strings.HasPrefix(rel, prefix) {
		return httpx.Forbidden("Access denied")
	}
	return m.serveFile(w, r, filepath.Join(m.a.Cfg.StoragePath, rel), filepath.Base(rel), true)
}

func (m *Module) attachmentFile(r *http.Request) (string, string, error) {
	u := user(r)
	id := r.PathValue("attachment_id")
	var rel, owner string
	var name *string
	err := m.a.DB.QueryRowContext(r.Context(), `SELECT a.file_path, a.filename, c.user_id FROM message_attachments a
		JOIN messages msg ON msg.id = a.message_id JOIN chats c ON c.id = msg.chat_id WHERE a.id = ?`, id).Scan(&rel, &name, &owner)
	if errors.Is(err, sql.ErrNoRows) {
		err = m.a.DB.QueryRowContext(r.Context(), `SELECT a.file_path, a.filename, ch.user_id FROM channel_message_attachments a
			JOIN channel_messages cm ON cm.id = a.message_id JOIN channels ch ON ch.id = cm.channel_id WHERE a.id = ?`, id).Scan(&rel, &name, &owner)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", httpx.NotFound("Attachment not found")
	}
	if err != nil {
		return "", "", err
	}
	if owner != u.ID {
		return "", "", httpx.Forbidden("Access denied")
	}
	full := filepath.Clean(filepath.Join(m.a.Cfg.StoragePath, filepath.FromSlash(rel)))
	if !strings.HasPrefix(full, filepath.Clean(m.a.Cfg.StoragePath)+string(filepath.Separator)) {
		return "", "", httpx.Forbidden("Access denied")
	}
	return full, firstNonEmpty(db.Deref(name), filepath.Base(full)), nil
}

func (m *Module) attachmentPreview(w http.ResponseWriter, r *http.Request) error {
	full, name, err := m.attachmentFile(r)
	if err != nil {
		return err
	}
	return m.serveFile(w, r, full, name, true)
}

func (m *Module) attachmentDownload(w http.ResponseWriter, r *http.Request) error {
	full, name, err := m.attachmentFile(r)
	if err != nil {
		return err
	}
	return m.serveFile(w, r, full, name, false)
}

func (m *Module) serveFile(w http.ResponseWriter, r *http.Request, full, name string, inline bool) error {
	f, err := os.Open(full)
	if err != nil {
		return httpx.NotFound("File not found")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		return httpx.NotFound("File not found")
	}
	disp := "attachment"
	if inline {
		disp = "inline"
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	ascii := strings.Map(func(r rune) rune {
		if r > 127 || r == '"' {
			return -1
		}
		return r
	}, name)
	if ascii == "" {
		ascii = "file"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disp, ascii, urlPathEscape(name)))
	w.Header().Set("Content-Type", guessMIME(name))
	http.ServeContent(w, r, name, st.ModTime(), f)
	return nil
}

func urlPathEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// --- search ---

func (m *Module) searchChats(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len([]rune(q)) < 2 || len(q) > 200 {
		return httpx.Unprocessable("Query must contain at least 2 non-whitespace characters")
	}
	limit := httpx.QueryInt(r, "limit", 50, 1, 200)
	perChat := httpx.QueryInt(r, "per_chat_limit", 5, 1, 50)
	res, err := m.search(r.Context(), u.ID, q, limit, perChat)
	if err != nil {
		return err
	}
	return httpx.OK(w, res)
}
