package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"agentrove/internal/db"
	"agentrove/internal/httpx"

	"github.com/google/uuid"
)

type Channel struct {
	ID          string
	UserID      string
	WorkspaceID string
	Name        string
	Worktree    bool
	Branch      *string
	CreatedAt   db.Time
	UpdatedAt   db.Time
	Members     []*Member
}

type Member struct {
	ID             string  `json:"id"`
	ChannelID      string  `json:"-"`
	ChatID         string  `json:"chat_id"`
	PermissionMode string  `json:"permission_mode"`
	ModelID        string  `json:"model_id"`
	Persona        *string `json:"persona"`
	ThinkingMode   *string `json:"thinking_mode"`
	DisplayName    string  `json:"display_name"`
	Introduced     bool    `json:"-"`
}

type Attachment struct {
	ID        string  `json:"id"`
	MessageID string  `json:"message_id"`
	FileURL   string  `json:"file_url"`
	FilePath  string  `json:"-"`
	FileType  string  `json:"file_type"`
	Filename  string  `json:"filename"`
	CreatedAt db.Time `json:"created_at"`
}

type Message struct {
	ID              string       `json:"id"`
	ChannelID       string       `json:"channel_id"`
	Seq             int64        `json:"seq"`
	Version         int          `json:"version"`
	MemberID        *string      `json:"member_id"`
	SourceMessageID *string      `json:"-"`
	Content         string       `json:"content"`
	ToolCallCount   int          `json:"tool_call_count"`
	DurationMS      *int64       `json:"duration_ms"`
	Audience        []string     `json:"audience"` // nil broadcasts
	Attachments     []Attachment `json:"attachments"`
	Status          string       `json:"status"`
	CreatedAt       db.Time      `json:"created_at"`
	AuthorType      string       `json:"author_type"`
}

func (m *Message) finish() {
	m.AuthorType = "user"
	if m.MemberID != nil {
		m.AuthorType = "agent"
	}
	if m.Attachments == nil {
		m.Attachments = []Attachment{}
	}
}

// Read is the ChannelRead response shape.
type Read struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Name         string    `json:"name"`
	Worktree     bool      `json:"worktree"`
	Branch       *string   `json:"branch"`
	CreatedAt    db.Time   `json:"created_at"`
	UpdatedAt    db.Time   `json:"updated_at"`
	Members      []*Member `json:"members"`
	AnchorChatID string    `json:"anchor_chat_id"`
	Activity     Activity  `json:"activity"`
}

type Activity struct {
	Epoch             string   `json:"epoch"`
	Version           int      `json:"version"`
	MemberIDs         []string `json:"member_ids"`
	WaitingMemberIDs  []string `json:"waiting_member_ids"`
	RetryingMemberIDs []string `json:"retrying_member_ids"`
}

// anchorChatID is the member chat owning the shared sandbox/worktree.
func (c *Channel) anchorChatID() string {
	anchor := ""
	for _, m := range c.Members {
		if anchor == "" || m.ChatID < anchor {
			anchor = m.ChatID
		}
	}
	return anchor
}

func (c *Channel) member(id string) *Member {
	for _, m := range c.Members {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func audienceValue(a []string) any {
	if a == nil {
		return nil
	}
	b, _ := json.Marshal(a)
	return string(b)
}

const channelCols = `id, user_id, workspace_id, name, worktree, branch, created_at, updated_at`
const memberCols = `id, channel_id, chat_id, permission_mode, model_id, persona, thinking_mode, display_name, introduced`
const messageCols = `id, channel_id, seq, version, member_id, source_message_id, content, tool_call_count, duration_ms, audience, status, created_at`

func scanChannel(row interface{ Scan(...any) error }) (*Channel, error) {
	c := &Channel{}
	err := row.Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.Name, &c.Worktree, &c.Branch, &c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func scanMessage(row interface{ Scan(...any) error }) (*Message, error) {
	m := &Message{}
	var audience *string
	err := row.Scan(&m.ID, &m.ChannelID, &m.Seq, &m.Version, &m.MemberID, &m.SourceMessageID, &m.Content, &m.ToolCallCount,
		&m.DurationMS, &audience, &m.Status, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	if audience != nil {
		if err := json.Unmarshal([]byte(*audience), &m.Audience); err != nil {
			return nil, err
		}
		if m.Audience == nil {
			m.Audience = []string{}
		}
	}
	m.finish()
	return m, nil
}

func loadMembers(ctx context.Context, q db.Querier, channelIDs ...string) (map[string][]*Member, error) {
	out := map[string][]*Member{}
	if len(channelIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(channelIDs))
	for i, id := range channelIDs {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx, `SELECT `+memberCols+` FROM channel_members WHERE channel_id IN (`+db.Placeholders(len(args))+`) ORDER BY created_at, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m := &Member{}
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.ChatID, &m.PermissionMode, &m.ModelID, &m.Persona, &m.ThinkingMode, &m.DisplayName, &m.Introduced); err != nil {
			return nil, err
		}
		out[m.ChannelID] = append(out[m.ChannelID], m)
	}
	return out, rows.Err()
}

func (s *Service) loadChannels(ctx context.Context, where string, args ...any) ([]*Channel, error) {
	rows, err := s.a.DB.QueryContext(ctx, `SELECT `+channelCols+` FROM channels `+where, args...)
	if err != nil {
		return nil, err
	}
	var chans []*Channel
	var ids []string
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		chans = append(chans, c)
		ids = append(ids, c.ID)
	}
	rows.Close()
	members, err := loadMembers(ctx, s.a.DB, ids...)
	if err != nil {
		return nil, err
	}
	for _, c := range chans {
		c.Members = members[c.ID]
		if c.Members == nil {
			c.Members = []*Member{}
		}
	}
	return chans, nil
}

// Get returns a channel owned by userID.
func (s *Service) Get(ctx context.Context, id, userID string) (*Channel, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, httpx.NotFound("Channel not found")
	}
	chans, err := s.loadChannels(ctx, `WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return nil, err
	}
	if len(chans) == 0 {
		return nil, httpx.NotFound("Channel not found")
	}
	return chans[0], nil
}

func (s *Service) List(ctx context.Context, userID string, workspaceID string) ([]*Channel, error) {
	if workspaceID != "" {
		return s.loadChannels(ctx, `WHERE user_id = ? AND workspace_id = ? ORDER BY updated_at DESC`, userID, workspaceID)
	}
	return s.loadChannels(ctx, `WHERE user_id = ? ORDER BY updated_at DESC`, userID)
}

func (s *Service) loadAttachments(ctx context.Context, msgs []*Message) error {
	if len(msgs) == 0 {
		return nil
	}
	byID := map[string]*Message{}
	args := make([]any, 0, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
		args = append(args, m.ID)
	}
	// Chunk to stay under SQLite's variable limit on long histories.
	for start := 0; start < len(args); start += 500 {
		end := min(start+500, len(args))
		chunk := args[start:end]
		rows, err := s.a.DB.QueryContext(ctx, `SELECT id, message_id, file_url, file_path, file_type, filename, created_at
			FROM channel_message_attachments WHERE message_id IN (`+db.Placeholders(len(chunk))+`) ORDER BY created_at, id`, chunk...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a Attachment
			if err := rows.Scan(&a.ID, &a.MessageID, &a.FileURL, &a.FilePath, &a.FileType, &a.Filename, &a.CreatedAt); err != nil {
				rows.Close()
				return err
			}
			if m := byID[a.MessageID]; m != nil {
				m.Attachments = append(m.Attachments, a)
			}
		}
		rows.Close()
	}
	return nil
}

func (s *Service) queryMessages(ctx context.Context, q string, args ...any) ([]*Message, error) {
	rows, err := s.a.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	out := []*Message{}
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.loadAttachments(ctx, out)
}

// Messages returns the channel history after afterSeq, oldest first.
func (s *Service) Messages(ctx context.Context, channelID string, afterSeq int64) ([]*Message, error) {
	return s.queryMessages(ctx, `SELECT `+messageCols+` FROM channel_messages WHERE channel_id = ? AND seq > ? ORDER BY seq`, channelID, afterSeq)
}

// unseen are completed messages by others not yet delivered to member.
func (s *Service) unseen(ctx context.Context, member *Member) ([]*Message, error) {
	return s.queryMessages(ctx, `SELECT `+messageCols+` FROM channel_messages m
		WHERE m.channel_id = ? AND m.status = 'completed' AND (m.member_id IS NULL OR m.member_id != ?)
		AND NOT EXISTS (SELECT 1 FROM channel_deliveries d WHERE d.member_id = ? AND d.message_id = m.id)
		ORDER BY m.seq`, member.ChannelID, member.ID, member.ID)
}

func markDelivered(ctx context.Context, q db.Querier, memberID string, msgs []*Message) error {
	for _, m := range msgs {
		if _, err := q.ExecContext(ctx, `INSERT OR IGNORE INTO channel_deliveries (member_id, message_id) VALUES (?, ?)`, memberID, m.ID); err != nil {
			return err
		}
	}
	return nil
}

// writeMessage persists the mutable fields of a streaming/finished message.
func writeMessage(ctx context.Context, q db.Querier, m *Message, content, status string) error {
	m.Content = content
	m.Status = status
	m.Version++
	_, err := q.ExecContext(ctx, `UPDATE channel_messages SET content = ?, status = ?, version = ?, tool_call_count = ?, duration_ms = ?,
		audience = ?, updated_at = ? WHERE id = ?`, content, status, m.Version, m.ToolCallCount, m.DurationMS, audienceValue(m.Audience), db.Now(), m.ID)
	return err
}

// insertMessage appends a message with the next seq and bumps the channel.
func (s *Service) insertMessage(ctx context.Context, ch *Channel, content string, memberID *string, attachments []savedFile, sourceMessageID *string, toolCalls int) (*Message, error) {
	now := db.Now()
	msg := &Message{
		ID: uuid.NewString(), ChannelID: ch.ID, Version: 1, MemberID: memberID, SourceMessageID: sourceMessageID,
		Content: content, ToolCallCount: toolCalls, CreatedAt: now, Status: "completed",
	}
	if memberID != nil {
		msg.Status = "streaming"
	} else {
		msg.Audience = userAudience(ch.Members, content)
	}
	err := s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET updated_at = ? WHERE id = ?`, now, ch.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM channel_messages WHERE channel_id = ?`, ch.ID).Scan(&msg.Seq); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_messages (`+messageCols+`, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			msg.ID, msg.ChannelID, msg.Seq, msg.Version, msg.MemberID, msg.SourceMessageID, msg.Content, msg.ToolCallCount,
			msg.DurationMS, audienceValue(msg.Audience), msg.Status, msg.CreatedAt, now); err != nil {
			return err
		}
		for _, f := range attachments {
			att := Attachment{ID: uuid.NewString(), MessageID: msg.ID, FilePath: f.FilePath, FileType: f.FileType, Filename: f.Filename, CreatedAt: now}
			att.FileURL = s.a.Cfg.APIPrefix + "/attachments/" + att.ID + "/preview"
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_message_attachments (id, message_id, file_url, file_path, file_type, filename, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, att.ID, att.MessageID, att.FileURL, att.FilePath, att.FileType, att.Filename, now, now); err != nil {
				return err
			}
			msg.Attachments = append(msg.Attachments, att)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ch.UpdatedAt = now
	msg.finish()
	return msg, nil
}

// sourceDuration is the member turn's duration from its assistant message.
func (s *Service) sourceDuration(ctx context.Context, q db.Querier, messageID string) (*int64, error) {
	var dur *int64
	var created db.Time
	err := q.QueryRowContext(ctx, `SELECT duration_ms, created_at FROM messages WHERE id = ?`, messageID).Scan(&dur, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dur == nil {
		d := time.Since(created.Time).Milliseconds()
		dur = &d
	}
	return dur, nil
}
