package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/automation"
	"agentrove/internal/db"
	"agentrove/internal/models"
)

// --- workspaces, models, chats ---

func (m *Module) listWorkspaces(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	ws, err := m.workspaces(ctx, c.userID, 100)
	if err != nil {
		return nil, err
	}
	return map[string]any{"workspaces": ws}, nil
}

// workspaces lists the user's workspaces, most recently active first.
func (m *Module) workspaces(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT w.id, w.name FROM workspaces w
		LEFT JOIN chats c ON c.workspace_id = w.id AND c.deleted_at IS NULL AND c.channel_id IS NULL
		WHERE w.user_id = ? AND w.deleted_at IS NULL
		GROUP BY w.id ORDER BY MAX(c.updated_at) IS NULL, MAX(c.updated_at) DESC, w.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name})
	}
	return out, rows.Err()
}

func (m *Module) resolveWorkspace(ctx context.Context, userID string, id *string) (string, error) {
	if id != nil && *id != "" {
		return *id, nil
	}
	ws, err := m.workspaces(ctx, userID, 1)
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "", errorf("No workspaces found — create one in Bitbox first.")
	}
	return ws[0]["id"].(string), nil
}

func (m *Module) listModels(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		AgentKind *string `json:"agent_kind"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list := []map[string]any{}
	if a.AgentKind == nil || *a.AgentKind == "" || *a.AgentKind == "claude" {
		for _, mi := range models.ClaudeModels {
			list = append(list, map[string]any{"model_id": mi.ID, "name": mi.Name, "agent_kind": mi.AgentKind, "thinking_modes": mi.EffortLevels})
		}
	}
	return map[string]any{"models": list}, nil
}

func (m *Module) listChats(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	a := struct {
		WorkspaceID *string `json:"workspace_id"`
		Page        int     `json:"page"`
		PerPage     int     `json:"per_page"`
	}{Page: 1, PerPage: 20}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	a.Page = max(a.Page, 1)
	a.PerPage = min(max(a.PerPage, 1), 100)
	where := `c.user_id = ? AND c.deleted_at IS NULL AND c.channel_id IS NULL`
	params := []any{c.userID}
	if a.WorkspaceID != nil && *a.WorkspaceID != "" {
		where += ` AND c.workspace_id = ?`
		params = append(params, *a.WorkspaceID)
	}
	var total int
	if err := m.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats c WHERE `+where, params...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := m.a.DB.QueryContext(ctx, `SELECT c.id, c.title, c.workspace_id, c.updated_at
		FROM chats c WHERE `+where+` ORDER BY c.pinned_at IS NULL, c.pinned_at DESC, c.updated_at DESC LIMIT ? OFFSET ?`,
		append(params, a.PerPage, (a.Page-1)*a.PerPage)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := []map[string]any{}
	for rows.Next() {
		var id, title, ws string
		var updated db.Time
		if err := rows.Scan(&id, &title, &ws, &updated); err != nil {
			return nil, err
		}
		chats = append(chats, map[string]any{"id": id, "title": title, "workspace_id": ws, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pages := (total + a.PerPage - 1) / a.PerPage
	return map[string]any{"chats": chats, "page": a.Page, "pages": pages, "total": total}, nil
}

// --- chat turns ---

func (m *Module) sendMessage(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Prompt       string  `json:"prompt"`
		ChatID       *string `json:"chat_id"`
		Title        *string `json:"title"`
		WorkspaceID  *string `json:"workspace_id"`
		ModelID      *string `json:"model_id"`
		ThinkingMode *string `json:"thinking_mode"`
		Worktree     bool    `json:"worktree"`
		BaseBranch   *string `json:"base_branch"`
		Persona      *string `json:"persona"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(a.Prompt); n < 1 || n > 100000 {
		return nil, errorf("prompt must be between 1 and 100000 characters")
	}
	if err := validEffort(a.ThinkingMode); err != nil {
		return nil, err
	}
	start := m.a.Hooks.StartChatTurn
	if start == nil {
		return nil, errorf("Chat runtime unavailable")
	}
	chatID := ""
	if a.ChatID != nil && *a.ChatID != "" {
		chat, err := m.a.Store.GetChat(ctx, c.userID, *a.ChatID)
		if err != nil {
			return nil, err
		}
		chatID = chat.ID
		// Inherit the last turn's settings when omitted.
		if a.ModelID == nil || *a.ModelID == "" {
			a.ModelID = chat.LastModelID
		}
		if a.ThinkingMode == nil || *a.ThinkingMode == "" {
			a.ThinkingMode = chat.LastThinkingMode
		}
		if a.Persona == nil || *a.Persona == "" {
			a.Persona = chat.LastPersonaName
		}
	}
	model, err := resolveModel(a.ModelID)
	if err != nil {
		return nil, err
	}
	created := false
	if chatID == "" {
		ws, err := m.resolveWorkspace(ctx, c.userID, a.WorkspaceID)
		if err != nil {
			return nil, err
		}
		title := a.Prompt
		if a.Title != nil && *a.Title != "" {
			title = *a.Title
		} else if r := []rune(title); len(r) > 60 {
			title = string(r[:60])
		}
		title = strings.TrimSpace(title)
		if title == "" {
			title = "New chat"
		}
		chat, err := automation.CreateChat(ctx, m.a, c.userID, ws, title, nil)
		if err != nil {
			return nil, err
		}
		chatID, created = chat.ID, true
	}
	persona := models.DefaultPersonaName
	if a.Persona != nil && *a.Persona != "" {
		persona = *a.Persona
	}
	var thinking *string
	if a.ThinkingMode != nil && *a.ThinkingMode != "" {
		thinking = a.ThinkingMode
	}
	var base *string
	if a.BaseBranch != nil && *a.BaseBranch != "" {
		base = a.BaseBranch
	}
	res, err := start(app.ChatTurnRequest{
		UserID: c.userID, ChatID: chatID, Prompt: a.Prompt, ModelID: model, PermissionMode: models.FullAccessPermissionMode,
		ThinkingMode: thinking, Worktree: a.Worktree, BaseBranch: base, SelectedPersonaName: persona, Unattended: true,
	})
	if err != nil {
		if created {
			automation.DeleteChat(context.WithoutCancel(ctx), m.a, chatID)
		}
		return nil, err
	}
	return map[string]any{"chat_id": chatID, "message_id": res.MessageID, "status": "in_progress"}, nil
}

func (m *Module) stopChat(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChatID string `json:"chat_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if c.chatID != "" && a.ChatID == c.chatID {
		return nil, errorf("Cannot stop the current chat from within its own turn.")
	}
	chat, err := m.a.Store.GetChat(ctx, c.userID, a.ChatID)
	if err != nil {
		return nil, err
	}
	if cancel := m.a.Hooks.CancelChatTurn; cancel != nil {
		if err := cancel(chat.ID); err != nil {
			return nil, err
		}
	}
	return map[string]any{"stopped": true}, nil
}

func encodeCursor(created db.Time, id string) string {
	b, _ := json.Marshal([]string{created.UTC().Format(time.RFC3339Nano), id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cur string) (db.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 2 {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	return db.T(t), parts[1], nil
}

func (m *Module) getMessages(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	a := struct {
		ChatID string  `json:"chat_id"`
		Limit  int     `json:"limit"`
		Cursor *string `json:"cursor"`
	}{Limit: 20}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	a.Limit = min(max(a.Limit, 1), 100)
	chat, err := m.a.Store.GetMemberChat(ctx, c.userID, a.ChatID)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, role, content_text, stream_status, created_at FROM messages WHERE chat_id = ? AND deleted_at IS NULL`
	params := []any{chat.ID}
	if a.Cursor != nil && *a.Cursor != "" {
		t, id, err := decodeCursor(*a.Cursor)
		if err != nil {
			return nil, err
		}
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		params = append(params, t, t, id)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	params = append(params, a.Limit+1)
	rows, err := m.a.DB.QueryContext(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, role, content, status string
		created                   db.Time
	}
	var page []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.role, &r.content, &r.status, &r.created); err != nil {
			rows.Close()
			return nil, err
		}
		page = append(page, r)
	}
	rows.Close()
	hasMore := len(page) > a.Limit
	if hasMore {
		page = page[:a.Limit]
	}
	var next any
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		next = encodeCursor(last.created, last.id)
	}
	// Newest-first from the DB; reverse for chronological reading.
	msgs := make([]map[string]any, 0, len(page))
	for i := len(page) - 1; i >= 0; i-- {
		r := page[i]
		msgs = append(msgs, map[string]any{"id": r.id, "role": r.role, "content": r.content, "stream_status": r.status, "created_at": r.created})
	}
	return map[string]any{"messages": msgs, "next_cursor": next, "has_more": hasMore, "context_usage": contextUsage(chat.ContextTokenUsage, chat.ContextWindow, chat.LastModelID)}, nil
}

func contextUsage(used, window int, modelID *string) map[string]any {
	if window <= 0 && modelID != nil {
		if mi, ok := models.LookupModel(*modelID); ok {
			window = mi.ContextWindow
		}
	}
	pct := 0.0
	if window > 0 {
		pct = min(float64(used)/float64(window)*100, 100)
	}
	return map[string]any{"tokens_used": used, "context_window": window, "percentage": pct}
}
