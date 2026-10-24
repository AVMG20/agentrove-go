package chat

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/gitops"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

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
// and removes worktrees they own. Their projects stay.
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
	// The projects stay, even with no chats left (a fresh one has none):
	// only the worktrees the chats owned go.
	for wsID := range workspaces {
		ws, err := m.a.Store.GetWorkspaceByID(ctx, wsID)
		if err != nil || ws.DeletedAt.Valid {
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
