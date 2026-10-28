package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

type Service struct {
	DB   *db.DB
	Cfg  *config.Config
	Keys *Keys
	// BoxSubnets are the sandbox networks' subnets, never trusted as proxies
	// (httpx.ClientIP). Filled in by the sandbox provider; nil = none.
	BoxSubnets *httpx.PrefixSet
}

type ctxKey struct{}

type sessionCtxKey struct{}

// UserFrom returns the authenticated user set by Middleware.
func UserFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(ctxKey{}).(*models.User)
	return u
}

// SessionFrom returns the caller's session id set by Require.
func SessionFrom(ctx context.Context) string {
	s, _ := ctx.Value(sessionCtxKey{}).(string)
	return s
}

func WithUser(ctx context.Context, u *models.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func (s *Service) GetUser(ctx context.Context, id string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.UserColumns+` FROM users WHERE id = ?`, id).Scan(u.ScanFields()...)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Service) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	u := &models.User{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+models.UserColumns+` FROM users WHERE email = ? COLLATE NOCASE`, strings.TrimSpace(email)).Scan(u.ScanFields()...)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// TokenFromRequest reads a bearer header, falling back to ?token= (SSE and
// WebSocket clients can't set headers).
func TokenFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if t, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(t)
		}
	}
	return r.URL.Query().Get("token")
}

// Authenticate resolves a session token to an active user. MCP tokens are
// rejected: they live in agent config inside the user's box, so they must
// not work as a login for the rest of the API (see AuthenticateMCP).
func (s *Service) Authenticate(ctx context.Context, token string) (*models.User, error) {
	u, _, err := s.authenticate(ctx, token, AudienceSession)
	return u, err
}

// AuthenticateMCP resolves a full-access MCP token; only the MCP endpoint
// accepts it.
func (s *Service) AuthenticateMCP(ctx context.Context, token string) (*models.User, error) {
	u, _, err := s.authenticate(ctx, token, AudienceMCP)
	return u, err
}

// authenticate verifies token for audience; a session token must also name a
// session that hasn't been signed out.
func (s *Service) authenticate(ctx context.Context, token, audience string) (*models.User, *Claims, error) {
	if token == "" {
		return nil, nil, errors.New("missing token")
	}
	c, err := s.Keys.VerifyClaims(token, audience)
	if err != nil {
		return nil, nil, err
	}
	if audience == AudienceSession {
		var one int
		if err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM sessions WHERE id = ? AND user_id = ?`, c.Session, c.Subject).Scan(&one); err != nil {
			return nil, nil, errors.New("session ended")
		}
	}
	u, err := s.GetUser(ctx, c.Subject)
	if err != nil {
		return nil, nil, err
	}
	if !u.IsActive {
		return nil, nil, errors.New("inactive user")
	}
	return u, c, nil
}

// Require wraps a handler so it only runs for authenticated users.
func (s *Service) Require(h httpx.HandlerFunc) http.HandlerFunc {
	return httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		u, c, err := s.authenticate(r.Context(), TokenFromRequest(r), AudienceSession)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			return httpx.Unauthorized("Could not validate credentials")
		}
		ctx := context.WithValue(WithUser(r.Context(), u), sessionCtxKey{}, c.Session)
		return h(w, r.WithContext(ctx))
	})
}

// CreateSession records a login and returns its id.
func (s *Service) CreateSession(ctx context.Context, userID, userAgent, ip string) (string, error) {
	id := uuid.NewString()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id, user_id, user_agent, ip_address, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, userID, truncate(userAgent, 512), truncate(ip, 45), db.Now())
	return id, err
}

// IssueSessionToken signs the token of a session. It is valid for SessionTTL
// or until the session is signed out, whichever comes first.
func (s *Service) IssueSessionToken(userID, sessionID string) (string, error) {
	return s.Keys.issue(Claims{Session: sessionID}, userID, AudienceSession, s.Cfg.SessionTTL)
}

// IssueMCPToken mints the token handed to the agentrove MCP server of a chat
// session; it authenticates as the chat's owner on the MCP endpoint only.
func (s *Service) IssueMCPToken(userID string) (string, error) {
	return s.Keys.IssueToken(userID, AudienceMCP, s.Cfg.MCPTokenLifetime)
}

// EndSession signs one session out; its tokens stop working at once.
func (s *Service) EndSession(ctx context.Context, sessionID string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
	return err
}

// EndAllSessions signs the user out everywhere.
func (s *Service) EndAllSessions(ctx context.Context, q db.Querier, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

// ResetPassword sets a new password hash for the holder of a valid reset
// token and signs them out everywhere, so a reset locks out whoever had the
// old password. ok=false means the token was invalid or expired.
func (s *Service) ResetPassword(ctx context.Context, token, hash string) (ok bool, err error) {
	err = s.DB.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		var userID string
		err := tx.QueryRowContext(ctx, `UPDATE users SET hashed_password = ?, reset_token = NULL, reset_token_expires = NULL, updated_at = ?
			WHERE reset_token = ? AND reset_token_expires > ? RETURNING id`, hash, now, HashToken(token), now).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		ok = true
		return s.EndAllSessions(ctx, tx, userID)
	})
	return ok && err == nil, err
}

// PurgeExpiredSessions drops sessions whose token has expired.
func (s *Service) PurgeExpiredSessions(ctx context.Context) {
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE created_at < ?`, db.T(time.Now().Add(-s.Cfg.SessionTTL)))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
