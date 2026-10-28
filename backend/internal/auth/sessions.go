package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// Session is one login of a user.
type Session struct {
	ID        string  `json:"id"`
	UserAgent string  `json:"user_agent"`
	IPAddress string  `json:"ip_address"`
	CreatedAt db.Time `json:"created_at"`
	Current   bool    `json:"current"`
}

var ErrSessionNotFound = errors.New("session not found")

// ListSessions returns the user's unexpired sessions, newest first.
func (s *Service) ListSessions(ctx context.Context, userID, currentID string) ([]Session, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, COALESCE(user_agent, ''), COALESCE(ip_address, ''), created_at
		FROM sessions WHERE user_id = ? AND created_at > ? ORDER BY created_at DESC`, userID, db.T(time.Now().Add(-s.Cfg.SessionTTL)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ID, &x.UserAgent, &x.IPAddress, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.Current = x.ID == currentID
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeSession ends one session of the user.
func (s *Service) RevokeSession(ctx context.Context, userID, sessionID string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id = ?`, userID, sessionID)
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

// RevokeOtherSessions ends every session of the user but keepID.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keepID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id != ?`, userID, keepID)
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
	if err := s.RevokeOtherSessions(r.Context(), UserFrom(r.Context()).ID, SessionFrom(r.Context())); err != nil {
		return err
	}
	return httpx.NoContent(w)
}
