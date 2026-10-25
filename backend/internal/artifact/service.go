// Package artifact serves agent-published HTML pages (/api/v1/artifacts) and
// the service the MCP artifact tools call.
package artifact

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"agentrove/internal/db"
	"agentrove/internal/httpx"

	"github.com/google/uuid"
)

// MaxHTMLLength bounds one page so a single agent call can't bloat the DB.
const MaxHTMLLength = 2_000_000

type Artifact struct {
	ID        string      `json:"id"`
	UserID    string      `json:"-"`
	ChatID    *string     `json:"chat_id"`
	Title     string      `json:"title"`
	HTML      string      `json:"-"`
	IsPublic  bool        `json:"is_public"`
	Version   int         `json:"version"`
	PinnedAt  db.NullTime `json:"pinned_at"`
	CreatedAt db.Time     `json:"created_at"`
	UpdatedAt db.Time     `json:"updated_at"`
}

// Detail is the response shape that includes the HTML.
type Detail struct {
	Artifact
	HTML string `json:"html"`
}

func (a *Artifact) Detail() Detail { return Detail{Artifact: *a, HTML: a.HTML} }

const columns = `id, user_id, chat_id, title, html, is_public, version, pinned_at, created_at, updated_at`

func scan(row interface{ Scan(...any) error }) (*Artifact, error) {
	a := &Artifact{}
	err := row.Scan(&a.ID, &a.UserID, &a.ChatID, &a.Title, &a.HTML, &a.IsPublic, &a.Version, &a.PinnedAt, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

type Service struct{ DB *db.DB }

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func NotFound() *httpx.Error { return httpx.NotFound("Artifact not found") }

// ListOptions narrows List; zero values mean no filter and no limit.
type ListOptions struct {
	// Query matches the title, case-insensitively.
	Query  string
	ChatID string
	Limit  int
	Offset int
}

func (s *Service) List(ctx context.Context, userID string, o ListOptions) ([]*Artifact, error) {
	q := `SELECT ` + columns + ` FROM artifacts WHERE user_id = ?`
	args := []any{userID}
	if o.Query != "" {
		q += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, "%"+likeEscaper.Replace(o.Query)+"%")
	}
	if o.ChatID != "" {
		q += ` AND chat_id = ?`
		args = append(args, o.ChatID)
	}
	q += ` ORDER BY pinned_at IS NULL, pinned_at DESC, updated_at DESC`
	if o.Limit > 0 {
		q += ` LIMIT ? OFFSET ?`
		args = append(args, o.Limit, max(o.Offset, 0))
	}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Artifact{}
	for rows.Next() {
		a, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Get(ctx context.Context, id, userID string) (*Artifact, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, NotFound()
	}
	a, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM artifacts WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound()
	}
	return a, err
}

// Viewable returns a public artifact, or a private one to its owner or a
// view-token holder. Everyone else gets 404 so existence doesn't leak.
func (s *Service) Viewable(ctx context.Context, id, viewerID string, granted bool) (*Artifact, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, NotFound()
	}
	a, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+columns+` FROM artifacts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, NotFound()
	}
	if err != nil {
		return nil, err
	}
	if !(a.IsPublic || granted || (viewerID != "" && a.UserID == viewerID)) {
		return nil, NotFound()
	}
	return a, nil
}

func validTitle(t string) error {
	n := utf8.RuneCountInString(t)
	if n < 1 || n > 255 {
		return httpx.Unprocessable("title must be between 1 and 255 characters")
	}
	return nil
}

func validHTML(h string) error {
	n := utf8.RuneCountInString(h)
	if n < 1 || n > MaxHTMLLength {
		return httpx.Unprocessable("html must be between 1 and 2000000 characters")
	}
	return nil
}

// ErrChatNotFound is returned by Create for a chat id the user doesn't own.
var ErrChatNotFound = httpx.NotFound("Chat not found")

func (s *Service) Create(ctx context.Context, userID, title, html string, chatID *string) (*Artifact, error) {
	if err := validTitle(title); err != nil {
		return nil, err
	}
	if err := validHTML(html); err != nil {
		return nil, err
	}
	if chatID != nil {
		var n int
		err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, *chatID, userID).Scan(&n)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrChatNotFound
		}
	}
	now := db.Now()
	a := &Artifact{ID: uuid.NewString(), UserID: userID, ChatID: chatID, Title: title, HTML: html, Version: 1, CreatedAt: now, UpdatedAt: now}
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO artifacts (`+columns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.ID, a.UserID, a.ChatID, a.Title, a.HTML, a.IsPublic, a.Version, a.PinnedAt, a.CreatedAt, a.UpdatedAt); err != nil {
			return err
		}
		return saveVersion(ctx, tx, a)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

// Update applies the given fields. Title/HTML changes bump the version;
// visibility changes don't.
func (s *Service) Update(ctx context.Context, id, userID string, title, html *string, isPublic *bool) (*Artifact, error) {
	if title != nil {
		if err := validTitle(*title); err != nil {
			return nil, err
		}
	}
	if html != nil {
		if err := validHTML(*html); err != nil {
			return nil, err
		}
	}
	a, err := s.Get(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	changed := false
	if title != nil && *title != a.Title {
		a.Title, changed = *title, true
	}
	if html != nil && *html != a.HTML {
		a.HTML, changed = *html, true
	}
	if isPublic != nil {
		a.IsPublic = *isPublic
	}
	if changed {
		a.Version++
	}
	a.UpdatedAt = db.Now()
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE artifacts SET title = ?, html = ?, is_public = ?, version = ?, updated_at = ? WHERE id = ?`,
			a.Title, a.HTML, a.IsPublic, a.Version, a.UpdatedAt, a.ID); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return saveVersion(ctx, tx, a)
	})
	return a, err
}

// SetPinned pins or unpins without touching the version or updated_at: a pin
// is list order, not an edit.
func (s *Service) SetPinned(ctx context.Context, id, userID string, pinned bool) error {
	if _, err := s.Get(ctx, id, userID); err != nil {
		return err
	}
	var at db.NullTime
	if pinned {
		at = db.NullTime{Time: db.Now(), Valid: true}
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE artifacts SET pinned_at = ? WHERE id = ? AND user_id = ?`, at, id, userID)
	return err
}

func (s *Service) Delete(ctx context.Context, id, userID string) error {
	if _, err := s.Get(ctx, id, userID); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM artifacts WHERE id = ?`, id)
	return err
}
