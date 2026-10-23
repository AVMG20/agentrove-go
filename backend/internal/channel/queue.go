package channel

import (
	"context"
	"database/sql"
	"errors"

	"agentrove/internal/db"

	"github.com/google/uuid"
)

// QueuedMessage is a user message waiting for the channel to go idle.
type QueuedMessage struct {
	ID          string               `json:"id"`
	Content     string               `json:"content"`
	QueuedAt    db.Time              `json:"queued_at"`
	Attachments db.JSON[[]savedFile] `json:"attachments"`
	position    int
}

func (s *Service) queueAdd(ctx context.Context, channelID, content string, files []savedFile) (*QueuedMessage, error) {
	q := &QueuedMessage{ID: uuid.NewString(), Content: content, QueuedAt: db.Now()}
	if len(files) > 0 {
		q.Attachments = db.J(files)
	}
	err := s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(position), 0) + 1 FROM channel_queued_messages WHERE channel_id = ?`, channelID).Scan(&q.position); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO channel_queued_messages (id, channel_id, position, content, attachments, queued_at) VALUES (?, ?, ?, ?, ?, ?)`,
			q.ID, channelID, q.position, q.Content, q.Attachments, q.QueuedAt)
		return err
	})
	return q, err
}

func (s *Service) queueList(ctx context.Context, channelID string) ([]*QueuedMessage, error) {
	rows, err := s.a.DB.QueryContext(ctx, `SELECT id, content, queued_at, attachments, position FROM channel_queued_messages WHERE channel_id = ? ORDER BY position, queued_at`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*QueuedMessage{}
	for rows.Next() {
		q := &QueuedMessage{}
		if err := rows.Scan(&q.ID, &q.Content, &q.QueuedAt, &q.Attachments, &q.position); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Service) queueGet(ctx context.Context, channelID, id string) (*QueuedMessage, error) {
	q := &QueuedMessage{}
	err := s.a.DB.QueryRowContext(ctx, `SELECT id, content, queued_at, attachments, position FROM channel_queued_messages WHERE channel_id = ? AND id = ?`, channelID, id).
		Scan(&q.ID, &q.Content, &q.QueuedAt, &q.Attachments, &q.position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return q, err
}

func (s *Service) queueUpdate(ctx context.Context, channelID, id, content string) (*QueuedMessage, error) {
	res, err := s.a.DB.ExecContext(ctx, `UPDATE channel_queued_messages SET content = ? WHERE channel_id = ? AND id = ?`, content, channelID, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return s.queueGet(ctx, channelID, id)
}

// queueDelete removes and returns an item (nil when absent).
func (s *Service) queueDelete(ctx context.Context, channelID, id string) (*QueuedMessage, error) {
	q, err := s.queueGet(ctx, channelID, id)
	if err != nil || q == nil {
		return nil, err
	}
	res, err := s.a.DB.ExecContext(ctx, `DELETE FROM channel_queued_messages WHERE channel_id = ? AND id = ?`, channelID, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return q, nil
}

func (s *Service) queuePop(ctx context.Context, channelID string) (*QueuedMessage, error) {
	items, err := s.queueList(ctx, channelID)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return s.queueDelete(ctx, channelID, items[0].ID)
}

// queueRequeue puts an item back at the front so a failed send retries next.
func (s *Service) queueRequeue(ctx context.Context, channelID string, q *QueuedMessage) error {
	var first int
	if err := s.a.DB.QueryRowContext(ctx, `SELECT COALESCE(MIN(position), 1) FROM channel_queued_messages WHERE channel_id = ?`, channelID).Scan(&first); err != nil {
		return err
	}
	_, err := s.a.DB.ExecContext(ctx, `INSERT INTO channel_queued_messages (id, channel_id, position, content, attachments, queued_at) VALUES (?, ?, ?, ?, ?, ?)`,
		q.ID, channelID, first-1, q.Content, q.Attachments, q.QueuedAt)
	return err
}

func (q *QueuedMessage) files() []savedFile {
	if !q.Attachments.Valid {
		return nil
	}
	return q.Attachments.V
}
