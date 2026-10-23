package chat

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// --- search ---

const (
	searchContextBefore = 30
	searchSnippetMax    = 160
	searchMaxRows       = 5000
)

type searchMatch struct {
	MessageID     string  `json:"message_id"`
	Role          string  `json:"role"`
	SnippetBefore string  `json:"snippet_before"`
	SnippetMatch  string  `json:"snippet_match"`
	SnippetAfter  string  `json:"snippet_after"`
	CreatedAt     db.Time `json:"created_at"`
}

type searchResult struct {
	ChatID        string        `json:"chat_id"`
	ChatTitle     string        `json:"chat_title"`
	WorkspaceID   string        `json:"workspace_id"`
	WorkspaceName string        `json:"workspace_name"`
	Matches       []searchMatch `json:"matches"`
	MatchCount    int           `json:"match_count"`
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (m *Module) search(ctx context.Context, userID, q string, limit, perChat int) (map[string]any, error) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT m.id, m.chat_id, m.role, m.content_text, m.created_at, c.title, c.workspace_id, w.name
		FROM messages m JOIN chats c ON c.id = m.chat_id JOIN workspaces w ON w.id = c.workspace_id
		WHERE c.user_id = ? AND c.deleted_at IS NULL AND c.channel_id IS NULL AND m.deleted_at IS NULL
		AND m.content_text LIKE ? ESCAPE '\' ORDER BY m.created_at DESC LIMIT ?`,
		userID, "%"+escapeLike(q)+"%", searchMaxRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var order []string
	grouped := map[string]*searchResult{}
	n := 0
	truncated := false
	ql := strings.ToLower(q)
	for rows.Next() {
		n++
		var msgID, chatID, role, content, title, wsID, wsName string
		var created db.Time
		if err := rows.Scan(&msgID, &chatID, &role, &content, &created, &title, &wsID, &wsName); err != nil {
			return nil, err
		}
		res := grouped[chatID]
		if res == nil {
			if len(grouped) >= limit+1 {
				truncated = true
				break
			}
			res = &searchResult{ChatID: chatID, ChatTitle: title, WorkspaceID: wsID, WorkspaceName: wsName, Matches: []searchMatch{}}
			grouped[chatID] = res
			order = append(order, chatID)
		}
		res.MatchCount++
		if len(res.Matches) >= perChat {
			truncated = true
			continue
		}
		before, match, after := snippet(content, ql)
		res.Matches = append(res.Matches, searchMatch{MessageID: msgID, Role: role, SnippetBefore: before, SnippetMatch: match, SnippetAfter: after, CreatedAt: created})
	}
	if n == searchMaxRows || len(order) > limit {
		truncated = true
	}
	if len(order) > limit {
		order = order[:limit]
	}
	results := make([]*searchResult, 0, len(order))
	for _, id := range order {
		results = append(results, grouped[id])
	}
	return map[string]any{"results": results, "truncated": truncated}, nil
}

// snippet windows the content around the first case-insensitive match,
// working in runes so multi-byte text never splits.
func snippet(content, ql string) (string, string, string) {
	runes := []rune(content)
	lower := []rune(strings.ToLower(content))
	qr := []rune(ql)
	idx := -1
	if len(lower) == len(runes) {
		for i := 0; i+len(qr) <= len(lower); i++ {
			if string(lower[i:i+len(qr)]) == ql {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		if utf8.RuneCountInString(content) > searchSnippetMax {
			return string(runes[:searchSnippetMax]), "", ""
		}
		return content, "", ""
	}
	start := max(0, idx-searchContextBefore)
	end := min(len(runes), start+searchSnippetMax)
	matchEnd := min(len(runes), idx+len(qr))
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "…"
	}
	if end < len(runes) {
		suffix = "…"
	}
	after := ""
	if matchEnd < end {
		after = string(runes[matchEnd:end])
	}
	return prefix + string(runes[start:idx]), string(runes[idx:matchEnd]), after + suffix
}

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
