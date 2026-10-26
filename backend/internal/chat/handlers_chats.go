package chat

import (
	"context"
	"database/sql"
	"net/http"
	"strings"
	"time"

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
		Title          string  `json:"title"`
		ModelID        string  `json:"model_id"`
		WorkspaceID    string  `json:"workspace_id"`
		WorktreeChatID *string `json:"worktree_chat_id"`
		// ProfileID picks the provider profile; "" = the user's default.
		ProfileID string `json:"profile_id"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if body.Title == "" || len(body.Title) > 255 {
		return httpx.Unprocessable("title must be 1-255 characters")
	}
	c, err := m.CreateChat(r.Context(), u.ID, body.Title, body.WorkspaceID, body.WorktreeChatID, body.ProfileID, body.ModelID)
	if err != nil {
		return err
	}
	return httpx.Created(w, c)
}

// CreateChat inserts a chat and announces it. With worktreeChatID the new chat
// joins that chat's workspace and runs in its worktree, so it sees its changes.
// The chat runs on profileID ("" = the default profile, see newChatProfile).
func (m *Module) CreateChat(ctx context.Context, userID, title, workspaceID string, worktreeChatID *string, profileID, modelID string) (*models.Chat, error) {
	profile, err := m.newChatProfile(ctx, userID, profileID, modelID)
	if err != nil {
		return nil, err
	}
	var worktree *string
	if worktreeChatID != nil && *worktreeChatID != "" {
		src, err := m.a.Store.GetChat(ctx, userID, *worktreeChatID)
		if err != nil {
			return nil, httpx.NotFound("Chat not found")
		}
		workspaceID = src.WorkspaceID
		worktree = src.WorktreeCwd
	}
	ws, err := m.a.Store.GetWorkspace(ctx, userID, workspaceID)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	now := db.Now()
	_, err = m.a.DB.ExecContext(ctx, `INSERT INTO chats (id, title, user_id, workspace_id, worktree_cwd, profile_id, provider, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, title, userID, ws.ID, worktree, profile.ID, profile.Provider, now, now)
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
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT `+models.ChatColumns+`
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
		if err := rows.Scan(c.ScanFields()...); err != nil {
			return err
		}
		c.Finish()
		items = append(items, c)
	}
	return httpx.OK(w, models.NewPaginated(items, page, perPage, total))
}

func (m *Module) getChat(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	c, err := m.a.Store.GetMemberChat(r.Context(), u.ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	return httpx.OK(w, c)
}

func (m *Module) contextUsage(w http.ResponseWriter, r *http.Request) error {
	c, err := m.a.Store.GetChat(r.Context(), user(r).ID, r.PathValue("chat_id"))
	if err != nil {
		return err
	}
	window := c.ContextWindow
	if window == 0 {
		window = models.ModelContextWindow(c.Provider, db.Deref(c.LastModelID))
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
		// ProfileID switches an empty thread's profile; once it has messages
		// the profile is fixed (the client opens a new thread instead).
		ProfileID *string `json:"profile_id"`
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
	// Extra WHERE terms: a profile switch only lands on a thread still empty.
	where := ""
	if body.ProfileID != nil {
		if *body.ProfileID == "" {
			return httpx.Unprocessable("profile_id must not be empty")
		}
		p, err := m.newChatProfile(r.Context(), u.ID, *body.ProfileID, "")
		if err != nil {
			return err
		}
		if p.ID != c.ProfileID {
			// Hold the turn slot so no turn starts on the old profile while
			// the switch lands; the UPDATE below re-checks emptiness itself.
			if !m.reserve(c.ID) {
				return errProfileFixed
			}
			defer func() {
				m.unreserve(c.ID)
				// A message queued meanwhile was left to the slot's holder.
				go func() { _ = m.runQueued(c.ID, u.ID, false) }()
			}()
			sets = append(sets, "profile_id = ?", "provider = ?")
			args = append(args, p.ID, p.Provider)
			where = ` AND session_id IS NULL AND NOT EXISTS (SELECT 1 FROM messages WHERE chat_id = chats.id AND deleted_at IS NULL)`
		}
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
	res, err := m.a.DB.ExecContext(r.Context(), `UPDATE chats SET `+strings.Join(sets, ", ")+` WHERE id = ?`+where, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 && where != "" {
		return errProfileFixed
	}
	c, err = m.a.Store.GetChat(r.Context(), u.ID, c.ID)
	if err != nil {
		return err
	}
	return httpx.OK(w, c)
}

// errProfileFixed refuses a profile switch on a thread that has (or is
// starting) a turn.
var errProfileFixed = httpx.Conflict("This thread already has messages, so its profile is fixed. Start a new thread to use another profile.")

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
	rows, err := m.a.DB.QueryContext(r.Context(), `SELECT id FROM chats WHERE user_id = ? AND deleted_at IS NULL AND channel_id IS NULL`, u.ID)
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

// DeleteChats soft-deletes chats, stops their agents, and removes worktrees
// they own. Their projects stay.
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
			WHERE id IN (`+ph+`) AND user_id = ? AND deleted_at IS NULL AND channel_id IS NULL`, args...)
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
			if _, err := tx.ExecContext(ctx, `DELETE FROM chat_checkpoints WHERE chat_id = ?`, v.id); err != nil {
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
	// The projects stay, even with no chats left (a fresh one has none). A
	// chat worktree goes once no live chat runs in it: chats created with
	// worktree_chat_id share it, and the last one out removes it.
	removed := map[string]bool{}
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
			if v.workspace != wsID || !strings.HasPrefix(v.worktree, ".worktrees/") || removed[wsID+"\x00"+v.worktree] {
				continue
			}
			var users int
			if err := m.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats WHERE workspace_id = ? AND worktree_cwd = ? AND deleted_at IS NULL`,
				wsID, v.worktree).Scan(&users); err != nil || users > 0 {
				continue
			}
			removed[wsID+"\x00"+v.worktree] = true
			wt := v.worktree
			go gitops.RemoveWorktree(context.Background(), sb, wt)
		}
	}
	return nil
}
