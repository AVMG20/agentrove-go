package auth

import (
	"context"
	"errors"
	"net/http"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// Session is one login of a user: its active refresh token, whose
// session_id survives rotation.
type Session struct {
	ID        string  `json:"id"`
	UserAgent string  `json:"user_agent"`
	IPAddress string  `json:"ip_address"`
	CreatedAt db.Time `json:"created_at"`
	// LastUsedAt is the session's last refresh (the active row's updated_at),
	// so it trails real activity by at most the access token lifetime.
	LastUsedAt db.Time `json:"last_used_at"`
	Current    bool    `json:"current"`
}

var ErrSessionNotFound = errors.New("session not found")

// ListSessions returns the user's active sessions, most recently used first.
func (s *Service) ListSessions(ctx context.Context, userID, currentID string) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT session_id, COALESCE(user_agent, ''), COALESCE(ip_address, ''), created_at, updated_at
		FROM refresh_tokens WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ? ORDER BY updated_at DESC`, userID, db.Now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.UserAgent, &x.IPAddress, &x.CreatedAt, &x.LastUsedAt); err != nil {
			return nil, err
		}
		x.Current = x.ID == currentID
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeSession ends one active session of the user. The reason 'revoked'
// makes a later refresh with its token fail without counting as theft.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	now := db.Now()
	res, err := s.DB.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, revoked_reason = 'revoked', updated_at = ?
		WHERE user_id = ? AND session_id = ? AND revoked_at IS NULL AND expires_at > ?`, now, now, userID, sessionID, now)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrSessionNotFound
	}
	return nil
}

// RevokeOtherSessions ends every active session of the user but keepID.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepID string) error {
	now := db.Now()
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, revoked_reason = 'revoked', updated_at = ?
		WHERE user_id = ? AND session_id != ? AND revoked_at IS NULL`, now, now, userID, keepID)
	return err
}

func (s *Service) listSessions(w http.ResponseWriter, r *http.Request) error {
	list, err := s.ListSessions(r.Context(), UserFrom(r.Context()).ID, SessionFrom(r.Context()))
	if err != nil {
		return err
	}
	return httpx.OK(w, list)
}

func (s *Service) revokeSession(w http.ResponseWriter, r *http.Request) error {
	id := r.PathValue("id")
	if id == SessionFrom(r.Context()) {
		return httpx.BadRequest("Sign out to end the current session")
	}
	if err := s.RevokeSession(r.Context(), UserFrom(r.Context()).ID, id); err != nil {
		if errors.Is(err, ErrSessionNotFound) {
			return httpx.NotFound("Session not found")
		}
		return err
	}
	return httpx.NoContent(w)
}

func (s *Service) revokeOtherSessions(w http.ResponseWriter, r *http.Request) error {
	current := SessionFrom(r.Context())
	if current == "" {
		// Tokens from before session ids can't tell which session to keep.
		return httpx.Conflict("This session can't be identified yet; try again in a few minutes")
	}
	if err := s.RevokeOtherSessions(r.Context(), UserFrom(r.Context()).ID, current); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
