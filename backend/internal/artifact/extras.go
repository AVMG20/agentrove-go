package artifact

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// Version history, find-and-replace edits, page error reports and page state:
// what agents can't build into the page themselves.

const (
	// KeepVersions bounds history; each version holds a full page.
	KeepVersions = 20
	keepErrors   = 50
	// MaxErrorLength trims one reported message (stack traces run long).
	MaxErrorLength = 2000
	MaxStateKeys   = 100
	MaxStateKey    = 200
	MaxStateValue  = 64_000
)

func saveVersion(ctx context.Context, tx *sql.Tx, a *Artifact) error {
	if _, err := tx.ExecContext(ctx, `INSERT INTO artifact_versions (artifact_id, version, title, html, created_at) VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.Version, a.Title, a.HTML, a.UpdatedAt); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM artifact_versions WHERE artifact_id = ? AND version <= ?`, a.ID, a.Version-KeepVersions)
	return err
}

// Edit is one find-and-replace step of an edit_artifact call.
type Edit struct {
	Old        string `json:"old_string"`
	New        string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all"`
}

// ApplyEdits runs the edits in order; each Old must match exactly once
// unless ReplaceAll, so an edit never lands somewhere unintended.
func ApplyEdits(html string, edits []Edit) (string, error) {
	if len(edits) == 0 {
		return "", httpx.Unprocessable("edits must not be empty")
	}
	for i, e := range edits {
		if e.Old == "" {
			return "", httpx.Unprocessable(fmt.Sprintf("edit %d: old_string must not be empty", i+1))
		}
		switch n := strings.Count(html, e.Old); {
		case n == 0:
			return "", httpx.Unprocessable(fmt.Sprintf("edit %d: old_string not found (after the earlier edits); read the page with get_artifact", i+1))
		case n > 1 && !e.ReplaceAll:
			return "", httpx.Unprocessable(fmt.Sprintf("edit %d: old_string matches %d times; add surrounding text to make it unique, or set replace_all", i+1, n))
		}
		// Check the size before building it: replace_all of a short string by a
		// long one multiplies the page (an agent-sized request could allocate
		// terabytes). MaxHTMLLength counts runes, at most 4 bytes each.
		if grown := len(html) + strings.Count(html, e.Old)*(len(e.New)-len(e.Old)); grown > 4*MaxHTMLLength {
			return "", httpx.Unprocessable(fmt.Sprintf("edit %d: the page would exceed %d characters", i+1, MaxHTMLLength))
		}
		html = strings.ReplaceAll(html, e.Old, e.New)
	}
	return html, nil
}

func (s *Service) Edit(ctx context.Context, id, userID string, edits []Edit, title *string) (*Artifact, error) {
	a, err := s.Get(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	html, err := ApplyEdits(a.HTML, edits)
	if err != nil {
		return nil, err
	}
	return s.Update(ctx, id, userID, title, &html, nil)
}

type VersionInfo struct {
	Version   int     `json:"version"`
	Title     string  `json:"title"`
	CreatedAt db.Time `json:"created_at"`
}

func (s *Service) Versions(ctx context.Context, id, userID string) ([]VersionInfo, error) {
	if _, err := s.Get(ctx, id, userID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT version, title, created_at FROM artifact_versions WHERE artifact_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionInfo{}
	for rows.Next() {
		var v VersionInfo
		if err := rows.Scan(&v.Version, &v.Title, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// AtVersion returns the artifact as it was at version (still in history).
func (s *Service) AtVersion(ctx context.Context, id, userID string, version int) (*Artifact, error) {
	a, err := s.Get(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	if version == a.Version {
		return a, nil
	}
	err = s.DB.QueryRowContext(ctx, `SELECT title, html, created_at FROM artifact_versions WHERE artifact_id = ? AND version = ?`, id, version).
		Scan(&a.Title, &a.HTML, &a.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound(fmt.Sprintf("Version %d is not in this artifact's history", version))
	}
	if err != nil {
		return nil, err
	}
	a.Version = version
	return a, nil
}

// Restore publishes an earlier version's title and HTML as a new version.
func (s *Service) Restore(ctx context.Context, id, userID string, version int) (*Artifact, error) {
	old, err := s.AtVersion(ctx, id, userID, version)
	if err != nil {
		return nil, err
	}
	return s.Update(ctx, id, userID, &old.Title, &old.HTML, nil)
}

type PageError struct {
	Message   string  `json:"message"`
	CreatedAt db.Time `json:"created_at"`
}

// ReportErrors records script errors seen while the owner viewed a version;
// repeats of a message collapse into one row.
func (s *Service) ReportErrors(ctx context.Context, id, userID string, version int, messages []string) error {
	a, err := s.Get(ctx, id, userID)
	if err != nil {
		return err
	}
	if version != a.Version {
		// A viewer still showing an older version; its errors are moot.
		return nil
	}
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		for _, msg := range messages {
			msg = strings.TrimSpace(msg)
			if msg == "" {
				continue
			}
			if utf8.RuneCountInString(msg) > MaxErrorLength {
				msg = string([]rune(msg)[:MaxErrorLength]) + "…"
			}
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO artifact_errors (artifact_id, version, message, created_at) VALUES (?, ?, ?, ?)`,
				id, version, msg, now); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM artifact_errors WHERE artifact_id = ? AND id NOT IN
			(SELECT id FROM artifact_errors WHERE artifact_id = ? ORDER BY id DESC LIMIT ?)`, id, id, keepErrors)
		return err
	})
}

// Errors lists what viewers reported for the given version, oldest first.
func (s *Service) Errors(ctx context.Context, id string, version int) ([]PageError, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT message, created_at FROM artifact_errors WHERE artifact_id = ? AND version = ? ORDER BY id`, id, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PageError{}
	for rows.Next() {
		var e PageError
		if err := rows.Scan(&e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) State(ctx context.Context, id, userID string) (map[string]json.RawMessage, error) {
	if _, err := s.Get(ctx, id, userID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM artifact_state WHERE artifact_id = ? ORDER BY key`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]json.RawMessage{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = json.RawMessage(v)
	}
	return out, rows.Err()
}

// SetState stores one key's JSON value; a nil value deletes the key.
func (s *Service) SetState(ctx context.Context, id, userID, key string, value json.RawMessage) error {
	if n := utf8.RuneCountInString(key); n < 1 || n > MaxStateKey {
		return httpx.Unprocessable(fmt.Sprintf("key must be between 1 and %d characters", MaxStateKey))
	}
	if _, err := s.Get(ctx, id, userID); err != nil {
		return err
	}
	if value == nil || string(value) == "null" {
		_, err := s.DB.ExecContext(ctx, `DELETE FROM artifact_state WHERE artifact_id = ? AND key = ?`, id, key)
		return err
	}
	if !json.Valid(value) {
		return httpx.Unprocessable("value must be JSON")
	}
	if len(value) > MaxStateValue {
		return httpx.Unprocessable(fmt.Sprintf("value must be at most %d bytes of JSON", MaxStateValue))
	}
	return s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var keys int
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1), COALESCE(MAX(key = ?), 0) FROM artifact_state WHERE artifact_id = ?`, key, id).
			Scan(&keys, &exists); err != nil {
			return err
		}
		if !exists && keys >= MaxStateKeys {
			return httpx.Unprocessable(fmt.Sprintf("an artifact holds at most %d state keys", MaxStateKeys))
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO artifact_state (artifact_id, key, value, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (artifact_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			id, key, string(value), db.Now())
		return err
	})
}
