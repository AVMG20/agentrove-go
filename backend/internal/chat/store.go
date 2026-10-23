package chat

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

type Attachment struct {
	ID        string  `json:"id"`
	MessageID string  `json:"message_id"`
	FileURL   string  `json:"file_url"`
	FileType  string  `json:"file_type"`
	Filename  *string `json:"filename"`
	CreatedAt db.Time `json:"created_at"`
	filePath  string
}

type Message struct {
	ID                 string                  `json:"id"`
	ChatID             string                  `json:"chat_id"`
	ContentText        string                  `json:"content_text"`
	ContentRender      map[string]any          `json:"content_render"`
	LastSeq            int64                   `json:"last_seq"`
	ActiveStreamID     *string                 `json:"active_stream_id"`
	Role               string                  `json:"role"`
	ModelID            *string                 `json:"model_id"`
	StreamStatus       string                  `json:"stream_status"`
	DurationMs         *int64                  `json:"duration_ms"`
	CreatedAt          db.Time                 `json:"created_at"`
	Attachments        []Attachment            `json:"attachments"`
	CheckpointID       *string                 `json:"checkpoint_id"`
	ToolResultsOmitted bool                    `json:"tool_results_omitted"`
	TotalCostUSD       *float64                `json:"-"`
	raw                db.JSON[map[string]any] `json:"-"`
}

const (
	StatusInProgress  = "in_progress"
	StatusCompleted   = "completed"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"
)

type store struct {
	db *db.DB
}

func (s *store) createMessage(ctx context.Context, q db.Querier, chatID, role, content string, modelID *string, atts []models.Attachment) (*Message, error) {
	now := db.Now()
	m := &Message{ID: uuid.NewString(), ChatID: chatID, Role: role, ModelID: modelID, StreamStatus: StatusCompleted, CreatedAt: now}
	render := map[string]any{"events": []any{}}
	if role == "user" {
		m.ContentText = content
		render = map[string]any{"events": []any{map[string]any{"type": "user_text", "text": content}}}
	} else {
		m.StreamStatus = StatusInProgress
	}
	m.ContentRender = render
	_, err := q.ExecContext(ctx, `INSERT INTO messages (id, chat_id, content_text, content_render, role, model_id, stream_status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, m.ID, chatID, m.ContentText, db.J(render), role, modelID, m.StreamStatus, now, now)
	if err != nil {
		return nil, err
	}
	m.Attachments = []Attachment{}
	for _, a := range atts {
		id := uuid.NewString()
		url := "/api/v1/attachments/" + id + "/preview"
		_, err := q.ExecContext(ctx, `INSERT INTO message_attachments (id, message_id, file_url, file_path, file_type, filename, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, m.ID, url, db.Deref(a.FilePath), a.FileType, db.Deref(a.Filename), now, now)
		if err != nil {
			return nil, err
		}
		m.Attachments = append(m.Attachments, Attachment{ID: id, MessageID: m.ID, FileURL: url, FileType: a.FileType, Filename: a.Filename, CreatedAt: now})
	}
	return m, nil
}

func (s *store) softDeleteMessage(ctx context.Context, id string) {
	_, _ = s.db.ExecContext(ctx, `UPDATE messages SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`, db.Now(), id)
}

const messageColumns = `m.id, m.chat_id, m.content_text, m.content_render, m.last_seq, m.active_stream_id, m.role, m.model_id,
	m.stream_status, m.duration_ms, m.created_at, m.total_cost_usd`

func scanMessage(row interface{ Scan(...any) error }, extra ...any) (*Message, error) {
	m := &Message{}
	dest := append([]any{&m.ID, &m.ChatID, &m.ContentText, &m.raw, &m.LastSeq, &m.ActiveStreamID, &m.Role, &m.ModelID,
		&m.StreamStatus, &m.DurationMs, &m.CreatedAt, &m.TotalCostUSD}, extra...)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	m.ContentRender = m.raw.V
	if m.ContentRender == nil {
		m.ContentRender = map[string]any{"events": []any{}}
	}
	m.Attachments = []Attachment{}
	return m, nil
}

func (s *store) getMessage(ctx context.Context, id string) (*Message, error) {
	m, err := scanMessage(s.db.QueryRowContext(ctx, `SELECT `+messageColumns+` FROM messages m WHERE m.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Message not found")
	}
	return m, err
}

// latestAssistant returns stream metadata of the newest assistant message.
func (s *store) latestAssistant(ctx context.Context, chatID string) (*Message, error) {
	m := &Message{}
	err := s.db.QueryRowContext(ctx, `SELECT id, model_id, stream_status, active_stream_id, last_seq FROM messages
		WHERE chat_id = ? AND role = 'assistant' AND deleted_at IS NULL ORDER BY created_at DESC, id DESC LIMIT 1`, chatID).
		Scan(&m.ID, &m.ModelID, &m.StreamStatus, &m.ActiveStreamID, &m.LastSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

func (s *store) inProgressAssistant(ctx context.Context, chatID string) (*Message, error) {
	m, err := s.latestAssistant(ctx, chatID)
	if err != nil || m == nil || m.StreamStatus != StatusInProgress {
		return nil, err
	}
	return m, nil
}

// appendEvents allocates a contiguous, gapless chat-scoped seq range and
// stores the events; returns the last seq.
func (s *store) appendEvents(ctx context.Context, chatID, messageID, streamID string, kinds []string, payloads []map[string]any, recordActivity bool) (int64, error) {
	var end int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		q := `UPDATE chats SET last_event_seq = last_event_seq + ? WHERE id = ? RETURNING last_event_seq`
		if recordActivity {
			q = `UPDATE chats SET last_event_seq = last_event_seq + ?, updated_at = ? WHERE id = ? RETURNING last_event_seq`
		}
		var err error
		if recordActivity {
			err = tx.QueryRowContext(ctx, q, len(kinds), db.Now(), chatID).Scan(&end)
		} else {
			err = tx.QueryRowContext(ctx, q, len(kinds), chatID).Scan(&end)
		}
		if err != nil {
			return fmt.Errorf("allocate seq: %w", err)
		}
		start := end - int64(len(kinds)) + 1
		now := db.Now()
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO message_events (message_id, chat_id, stream_id, seq, event_type, render_payload, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for i := range kinds {
			if _, err := stmt.ExecContext(ctx, messageID, chatID, streamID, start+int64(i), kinds[i], db.J(payloads[i]), now); err != nil {
				return err
			}
		}
		return nil
	})
	return end, err
}

type snapshot struct {
	ContentText    string
	Render         map[string]any
	LastSeq        int64
	ActiveStreamID *string
	Status         string // "" leaves status unchanged
	TotalCostUSD   *float64
	RecordActivity bool
}

// updateSnapshot writes the message's render checkpoint. A terminal status
// records the duration and prunes the event log (history renders from the
// snapshot from then on). Returns duration_ms when set.
func (s *store) updateSnapshot(ctx context.Context, messageID string, sn snapshot) (*int64, error) {
	var dur *int64
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		var createdAt db.Time
		if err := tx.QueryRowContext(ctx, `SELECT created_at FROM messages WHERE id = ?`, messageID).Scan(&createdAt); err != nil {
			return err
		}
		sets := []string{"content_text = ?", "content_render = ?", "last_seq = MAX(last_seq, ?)", "active_stream_id = ?", "updated_at = ?"}
		args := []any{sn.ContentText, db.J(sn.Render), sn.LastSeq, sn.ActiveStreamID, now}
		terminal := sn.Status != "" && sn.Status != StatusInProgress
		if sn.Status != "" {
			sets = append(sets, "stream_status = ?")
			args = append(args, sn.Status)
		}
		if terminal && sn.RecordActivity {
			d := now.Sub(createdAt.Time).Milliseconds()
			dur = &d
			sets = append(sets, "duration_ms = ?")
			args = append(args, d)
		}
		if sn.TotalCostUSD != nil {
			sets = append(sets, "total_cost_usd = ?")
			args = append(args, *sn.TotalCostUSD)
		}
		args = append(args, messageID)
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return err
		}
		if terminal {
			_, err := tx.ExecContext(ctx, `DELETE FROM message_events WHERE message_id = ?`, messageID)
			return err
		}
		return nil
	})
	return dur, err
}

type storedEvent struct {
	ChatID    string
	MessageID string
	StreamID  string
	Seq       int64
	Kind      string
	Payload   db.JSON[map[string]any]
	CreatedAt db.Time
}

func (s *store) chatEventsAfter(ctx context.Context, chatID string, after int64, limit int) ([]storedEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.chat_id, e.message_id, e.stream_id, e.seq, e.event_type, e.render_payload, e.created_at
		FROM message_events e WHERE e.chat_id = ? AND e.seq > ? ORDER BY e.seq LIMIT ?`, chatID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedEvent
	for rows.Next() {
		var e storedEvent
		if err := rows.Scan(&e.ChatID, &e.MessageID, &e.StreamID, &e.Seq, &e.Kind, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *store) messageEventsAfter(ctx context.Context, messageID string, after int64, limit int) ([]storedEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chat_id, message_id, stream_id, seq, event_type, render_payload, created_at
		FROM message_events WHERE message_id = ? AND seq > ? ORDER BY seq LIMIT ?`, messageID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storedEvent
	for rows.Next() {
		var e storedEvent
		if err := rows.Scan(&e.ChatID, &e.MessageID, &e.StreamID, &e.Seq, &e.Kind, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func encodeCursor(t db.Time, id string) string {
	return base64.URLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id))
}

func decodeCursor(c string) (time.Time, string, error) {
	b, err := base64.URLEncoding.DecodeString(c)
	if err != nil {
		return time.Time{}, "", err
	}
	ts, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, "", errors.New("bad cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		var dt db.Time
		if err2 := dt.Scan(ts); err2 != nil {
			return time.Time{}, "", err
		}
		t = dt.Time
	}
	return t, id, nil
}

func (s *store) listMessages(ctx context.Context, chatID, cursor string, limit int, omitToolResults bool) (models.CursorPaginated[*Message], error) {
	out := models.CursorPaginated[*Message]{Items: []*Message{}}
	q := `SELECT ` + messageColumns + `, cp.id FROM messages m LEFT JOIN chat_checkpoints cp ON cp.assistant_message_id = m.id
		WHERE m.chat_id = ? AND m.deleted_at IS NULL`
	args := []any{chatID}
	if cursor != "" {
		ts, id, err := decodeCursor(cursor)
		if err != nil {
			return out, httpx.BadRequest("Invalid pagination cursor")
		}
		q += ` AND (m.created_at < ? OR (m.created_at = ? AND m.id < ?))`
		args = append(args, db.T(ts), db.T(ts), id)
	}
	q += ` ORDER BY m.created_at DESC, m.id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	var msgs []*Message
	for rows.Next() {
		var cp *string
		m, err := scanMessage(rows, &cp)
		if err != nil {
			rows.Close()
			return out, err
		}
		m.CheckpointID = cp
		msgs = append(msgs, m)
	}
	rows.Close()
	if len(msgs) > limit {
		out.HasMore = true
		msgs = msgs[:limit]
	}
	if err := s.loadAttachments(ctx, msgs); err != nil {
		return out, err
	}
	for _, m := range msgs {
		if omitToolResults && m.Role == "assistant" && m.StreamStatus != StatusInProgress {
			omitResults(m)
		}
	}
	out.Items = msgs
	if out.HasMore && len(msgs) > 0 {
		last := msgs[len(msgs)-1]
		c := encodeCursor(last.CreatedAt, last.ID)
		out.NextCursor = &c
	}
	return out, nil
}

func (s *store) loadAttachments(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := map[string]*Message{}
	args := make([]any, 0, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
		args = append(args, m.ID)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, message_id, file_url, file_type, filename, created_at FROM message_attachments
		WHERE message_id IN (`+db.Placeholders(len(args))+`) ORDER BY created_at`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.FileURL, &a.FileType, &a.Filename, &a.CreatedAt); err != nil {
			return err
		}
		if m := byID[a.MessageID]; m != nil {
			m.Attachments = append(m.Attachments, a)
		}
	}
	return rows.Err()
}

// omitResults drops tool results from a finished turn's render: they dominate
// history size and are fetched per message on demand (/messages/{id}/render).
func omitResults(m *Message) {
	events, _ := m.ContentRender["events"].([]any)
	has := false
	for _, e := range events {
		if ev, ok := e.(map[string]any); ok {
			if tool, ok := ev["tool"].(map[string]any); ok {
				if _, ok := tool["result"]; ok {
					has = true
					break
				}
			}
		}
	}
	if !has {
		return
	}
	seen := map[any]map[string]any{}
	compact := make([]any, 0, len(events))
	for _, e := range events {
		ev, ok := e.(map[string]any)
		tool, isTool := ev["tool"].(map[string]any)
		if !ok || !isTool {
			compact = append(compact, e)
			continue
		}
		folded := seen[tool["id"]]
		if folded == nil {
			folded = map[string]any{}
			seen[tool["id"]] = folded
		}
		slim := map[string]any{}
		for k, v := range tool {
			if k == "result" {
				continue
			}
			if k == "input" || k == "title" {
				if v != nil && jsonEqual(folded[k], v) {
					continue
				}
				if v != nil {
					folded[k] = v
				}
			}
			slim[k] = v
		}
		copyEv := map[string]any{}
		for k, v := range ev {
			copyEv[k] = v
		}
		copyEv["tool"] = slim
		compact = append(compact, copyEv)
	}
	render := map[string]any{}
	for k, v := range m.ContentRender {
		render[k] = v
	}
	render["events"] = compact
	m.ContentRender = render
	m.ToolResultsOmitted = true
}

func jsonEqual(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Queue (replaces the Redis queue).

type QueuedMessage struct {
	ID                  string              `json:"id"`
	Content             string              `json:"content"`
	ModelID             string              `json:"model_id"`
	PermissionMode      string              `json:"permission_mode"`
	ThinkingMode        *string             `json:"thinking_mode"`
	Worktree            bool                `json:"worktree"`
	BaseBranch          *string             `json:"base_branch"`
	FastMode            bool                `json:"fast_mode"`
	SelectedPersonaName string              `json:"selected_persona_name"`
	QueuedAt            db.Time             `json:"queued_at"`
	Attachments         []models.Attachment `json:"attachments"`
	sendNow             bool
}

const queueColumns = `id, content, model_id, permission_mode, thinking_mode, worktree, base_branch, selected_persona_name, attachments, send_now, queued_at`

func scanQueued(row interface{ Scan(...any) error }) (*QueuedMessage, error) {
	q := &QueuedMessage{}
	var atts db.JSON[[]models.Attachment]
	if err := row.Scan(&q.ID, &q.Content, &q.ModelID, &q.PermissionMode, &q.ThinkingMode, &q.Worktree, &q.BaseBranch,
		&q.SelectedPersonaName, &atts, &q.sendNow, &q.QueuedAt); err != nil {
		return nil, err
	}
	if atts.Valid {
		q.Attachments = atts.V
	}
	return q, nil
}

func (s *store) enqueue(ctx context.Context, chatID string, q *QueuedMessage) error {
	var atts db.JSON[[]models.Attachment]
	if len(q.Attachments) > 0 {
		atts = db.J(q.Attachments)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO queued_messages (id, chat_id, position, content, model_id, permission_mode, thinking_mode,
		worktree, base_branch, selected_persona_name, attachments, send_now, queued_at)
		VALUES (?, ?, COALESCE((SELECT MAX(position) FROM queued_messages WHERE chat_id = ?), 0) + 1, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		q.ID, chatID, chatID, q.Content, q.ModelID, q.PermissionMode, q.ThinkingMode, q.Worktree, q.BaseBranch, q.SelectedPersonaName, atts, q.QueuedAt)
	return err
}

func (s *store) listQueue(ctx context.Context, chatID string) ([]*QueuedMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+queueColumns+` FROM queued_messages WHERE chat_id = ? ORDER BY position`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*QueuedMessage{}
	for rows.Next() {
		q, err := scanQueued(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// popQueued removes and returns the next message: a send-now one first; with
// sendNowOnly, nothing else.
func (s *store) popQueued(ctx context.Context, chatID string, sendNowOnly bool) (*QueuedMessage, error) {
	var out *QueuedMessage
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		q := `SELECT ` + queueColumns + ` FROM queued_messages WHERE chat_id = ?`
		if sendNowOnly {
			q += ` AND send_now = 1`
		}
		q += ` ORDER BY send_now DESC, position LIMIT 1`
		m, err := scanQueued(tx.QueryRowContext(ctx, q, chatID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM queued_messages WHERE id = ?`, m.ID); err != nil {
			return err
		}
		out = m
		return nil
	})
	return out, err
}

// requeue puts a message back at the head of the queue.
func (s *store) requeue(ctx context.Context, chatID string, q *QueuedMessage) {
	var atts db.JSON[[]models.Attachment]
	if len(q.Attachments) > 0 {
		atts = db.J(q.Attachments)
	}
	_, _ = s.db.ExecContext(ctx, `INSERT INTO queued_messages (id, chat_id, position, content, model_id, permission_mode, thinking_mode,
		worktree, base_branch, selected_persona_name, attachments, send_now, queued_at)
		VALUES (?, ?, COALESCE((SELECT MIN(position) FROM queued_messages WHERE chat_id = ?), 1) - 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		q.ID, chatID, chatID, q.Content, q.ModelID, q.PermissionMode, q.ThinkingMode, q.Worktree, q.BaseBranch, q.SelectedPersonaName, atts, q.sendNow, q.QueuedAt)
}

func (s *store) hasSendNow(ctx context.Context, chatID string) bool {
	var n int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM queued_messages WHERE chat_id = ? AND send_now = 1`, chatID).Scan(&n)
	return n > 0
}
