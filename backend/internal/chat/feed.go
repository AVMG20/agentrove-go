package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/httpx"

	"github.com/google/uuid"
)

const pingInterval = 15 * time.Second

// chatEventsFeed is the per-session chat lifecycle feed (chat_created,
// title_updated, stream_started, background_tasks). No replay: the sidebar
// refetches on reconnect.
func (m *Module) chatEventsFeed(w http.ResponseWriter, r *http.Request) error {
	sub := m.a.Hub.Subscribe(events.UserChats(user(r).ID), 256)
	defer sub.Close()
	sse, err := httpx.NewSSE(w)
	if err != nil {
		return err
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-sub.Dropped:
			return nil
		case <-ping.C:
			if sse.Send("ping", "", "") != nil {
				return nil
			}
		case msg := <-sub.C:
			if sse.Send("chat_event", "", string(msg)) != nil {
				return nil
			}
		}
	}
}

func parseCursors(raw string) (map[string]int64, error) {
	out := map[string]int64{}
	if raw == "" {
		return out, nil
	}
	var parsed map[string]json.Number
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return nil, httpx.Unprocessable("cursors must be a JSON object")
	}
	for k, v := range parsed {
		if _, err := uuid.Parse(k); err != nil {
			return nil, httpx.Unprocessable("cursor keys must be chat ids")
		}
		n, err := strconv.ParseInt(v.String(), 10, 64)
		if err != nil || n < 0 {
			return nil, httpx.Unprocessable("cursor seq must be a non-negative integer")
		}
		out[k] = n
	}
	return out, nil
}

// streamsFeed multiplexes every stream of the user onto one SSE connection.
// It subscribes first, replays each cursor chat's backlog from the DB, then
// forwards live envelopes, dropping duplicates by seq and backfilling gaps.
func (m *Module) streamsFeed(w http.ResponseWriter, r *http.Request) error {
	u := user(r)
	if len(r.URL.Query().Get("cursors")) > 8192 {
		return httpx.Unprocessable("cursors too long")
	}
	requested, err := parseCursors(r.URL.Query().Get("cursors"))
	if err != nil {
		return err
	}
	ctx := r.Context()
	last := map[string]int64{}
	for id, seq := range requested {
		if _, err := m.a.Store.GetMemberChat(ctx, u.ID, id); err == nil {
			last[id] = seq
		}
	}
	sub := m.a.Hub.Subscribe(events.UserStreams(u.ID), 4096)
	defer sub.Close()
	sse, err := httpx.NewSSE(w)
	if err != nil {
		return err
	}
	for id, seq := range last {
		n, err := m.replay(ctx, sse, id, seq)
		if err != nil {
			return nil
		}
		last[id] = n
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-sub.Dropped:
			// Fell behind: end the feed so the client reconnects with cursors.
			return nil
		case <-ping.C:
			if sse.Send("ping", "", "") != nil {
				return nil
			}
		case raw := <-sub.C:
			var env struct {
				ChatID    string `json:"chatId"`
				ChannelID string `json:"channelId"`
				Seq       *int64 `json:"seq"`
			}
			if json.Unmarshal(raw, &env) != nil {
				continue
			}
			if env.ChannelID != "" {
				if sse.Send("stream", "", string(raw)) != nil {
					return nil
				}
				continue
			}
			if env.ChatID == "" || env.Seq == nil {
				continue
			}
			seq := *env.Seq
			if prev, ok := last[env.ChatID]; ok {
				if seq <= prev {
					continue
				}
				if seq > prev+1 {
					n, err := m.replay(ctx, sse, env.ChatID, prev)
					if err != nil {
						return nil
					}
					last[env.ChatID] = n
					if n >= seq {
						continue
					}
				}
			}
			if sse.Send("stream", strconv.FormatInt(seq, 10), string(raw)) != nil {
				return nil
			}
			last[env.ChatID] = seq
		}
	}
}

// replay sends persisted events after seq and returns the last seq sent.
func (m *Module) replay(ctx context.Context, sse *httpx.SSE, chatID string, after int64) (int64, error) {
	for {
		evs, err := m.st.chatEventsAfter(ctx, chatID, after, 5000)
		if err != nil {
			slog.Warn("stream replay", "chat", chatID, "err", err)
			return after, nil
		}
		if len(evs) == 0 {
			return after, nil
		}
		for _, e := range evs {
			data := envelope(e.ChatID, e.MessageID, e.StreamID, e.Seq, e.Kind, e.Payload.V)
			if err := sse.Send("stream", strconv.FormatInt(e.Seq, 10), string(data)); err != nil {
				return after, err
			}
			after = e.Seq
		}
		if len(evs) < 5000 {
			return after, nil
		}
	}
}

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
