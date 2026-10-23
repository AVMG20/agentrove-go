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

// UserFrom returns the authenticated user set by Middleware.
func UserFrom(ctx context.Context) *models.User {
	u, _ := ctx.Value(ctxKey{}).(*models.User)
	return u
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
	return s.authenticate(ctx, token, AudienceSession)
}

// AuthenticateMCP resolves a full-access MCP token; only the MCP endpoint
// accepts it.
func (s *Service) AuthenticateMCP(ctx context.Context, token string) (*models.User, error) {
	return s.authenticate(ctx, token, AudienceMCP)
}

func (s *Service) authenticate(ctx context.Context, token, audience string) (*models.User, error) {
	if token == "" {
		return nil, errors.New("missing token")
	}
	sub, err := s.Keys.VerifyToken(token, audience)
	if err != nil {
		return nil, err
	}
	u, err := s.GetUser(ctx, sub)
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		return nil, errors.New("inactive user")
	}
	return u, nil
}

// Require wraps a handler so it only runs for authenticated users.
func (s *Service) Require(h httpx.HandlerFunc) http.HandlerFunc {
	return httpx.Wrap(func(w http.ResponseWriter, r *http.Request) error {
		u, err := s.Authenticate(r.Context(), TokenFromRequest(r))
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			return httpx.Unauthorized("Could not validate credentials")
		}
		return h(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

func (s *Service) IssueAccessToken(userID string) (string, error) {
	return s.Keys.IssueToken(userID, AudienceSession, s.Cfg.AccessTokenTTL)
}

// IssueMCPToken mints the token handed to the agentrove MCP server of a chat
// session; it authenticates as the chat's owner on the MCP endpoint only.
func (s *Service) IssueMCPToken(userID string) (string, error) {
	return s.Keys.IssueToken(userID, AudienceMCP, s.Cfg.MCPTokenLifetime)
}

func (s *Service) CreateRefreshToken(ctx context.Context, q db.Querier, userID, userAgent, ip string) (string, error) {
	token := RandomToken(32)
	now := db.Now()
	_, err := q.ExecContext(ctx, `INSERT INTO refresh_tokens (id, token_hash, user_id, expires_at, user_agent, ip_address, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), HashToken(token), userID, db.T(time.Now().Add(s.Cfg.RefreshTokenTTL)), truncate(userAgent, 512), truncate(ip, 45), now, now)
	return token, err
}

var ErrInvalidRefresh = errors.New("invalid or expired refresh token")

// rotationGrace is how long a token replaced by a refresh may be replayed
// without counting as theft: tabs (or a waking laptop) refresh at once, and
// the losers pick up the rotated token from storage (frontend/src/utils/storage.ts).
const rotationGrace = 60 * time.Second

// RotateRefreshToken validates token, revokes it and issues a replacement.
// Reusing a revoked token revokes every token of that user (theft signal),
// except a just-rotated one within rotationGrace, which only fails.
func (s *Service) RotateRefreshToken(ctx context.Context, token, userAgent, ip string) (*models.User, string, error) {
	var user *models.User
	var newToken, reusedBy string
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var id, userID string
		var expires db.Time
		var revoked db.NullTime
		var reason sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, user_id, expires_at, revoked_at, revoked_reason FROM refresh_tokens WHERE token_hash = ?`, HashToken(token)).
			Scan(&id, &userID, &expires, &revoked, &reason)
		if err != nil {
			return ErrInvalidRefresh
		}
		now := db.Now()
		if revoked.Valid {
			if reason.String != "rotated" || time.Since(revoked.Time.Time) > rotationGrace {
				reusedBy = userID
			}
			return ErrInvalidRefresh
		}
		if time.Now().After(expires.Time) {
			return ErrInvalidRefresh
		}
		if _, err := tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, revoked_reason = 'rotated', updated_at = ? WHERE id = ?`, now, now, id); err != nil {
			return err
		}
		u := &models.User{}
		if err := tx.QueryRowContext(ctx, `SELECT `+models.UserColumns+` FROM users WHERE id = ?`, userID).Scan(u.ScanFields()...); err != nil {
			return ErrInvalidRefresh
		}
		if !u.IsActive {
			return ErrInvalidRefresh
		}
		user = u
		newToken, err = s.CreateRefreshToken(ctx, tx, userID, userAgent, ip)
		return err
	})
	if reusedBy != "" {
		// Outside the tx: returning the error rolled it back, and the
		// theft-triggered revoke-all must persist.
		if rerr := s.RevokeAllRefreshTokens(ctx, s.DB, reusedBy); rerr != nil {
			return nil, "", rerr
		}
	}
	if err != nil {
		return nil, "", err
	}
	return user, newToken, nil
}

// RevokeAllRefreshTokens signs the user out everywhere (token reuse,
// password reset). Access tokens stay valid until they expire.
func (s *Service) RevokeAllRefreshTokens(ctx context.Context, q db.Querier, userID string) error {
	now := db.Now()
	_, err := q.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, updated_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, now, userID)
	return err
}

// ResetPassword sets a new password hash for the holder of a valid reset
// token and revokes their refresh tokens, so a reset locks out whoever had
// the old password. ok=false means the token was invalid or expired.
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
		return s.RevokeAllRefreshTokens(ctx, tx, userID)
	})
	return ok && err == nil, err
}

func (s *Service) RevokeRefreshToken(ctx context.Context, token string) error {
	now := db.Now()
	_, err := s.DB.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, updated_at = ? WHERE token_hash = ? AND revoked_at IS NULL`, now, now, HashToken(token))
	return err
}

// PurgeExpiredRefreshTokens drops tokens that can no longer be used.
func (s *Service) PurgeExpiredRefreshTokens(ctx context.Context) {
	cutoff := db.T(time.Now().Add(-7 * 24 * time.Hour))
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)`, db.Now(), cutoff)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
