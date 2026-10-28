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

// SessionFrom returns the caller's session id set by Require; "" for tokens
// issued without one.
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

func (s *Service) authenticate(ctx context.Context, token, audience string) (*models.User, *Claims, error) {
	if token == "" {
		return nil, nil, errors.New("missing token")
	}
	c, err := s.Keys.VerifyClaims(token, audience)
	if err != nil {
		return nil, nil, err
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

func (s *Service) IssueAccessToken(userID string) (string, error) {
	return s.issueSessionToken(userID, "")
}

// issueSessionToken is IssueAccessToken bound to a login's session id.
func (s *Service) issueSessionToken(userID, sessionID string) (string, error) {
	return s.Keys.issue(Claims{Session: sessionID}, userID, AudienceSession, s.Cfg.AccessTokenTTL)
}

// IssueMCPToken mints the token handed to the agentrove MCP server of a chat
// session; it authenticates as the chat's owner on the MCP endpoint only.
func (s *Service) IssueMCPToken(userID string) (string, error) {
	return s.Keys.IssueToken(userID, AudienceMCP, s.Cfg.MCPTokenLifetime)
}

// CreateRefreshToken starts a session (a login) and returns its refresh
// token and session id.
func (s *Service) CreateRefreshToken(ctx context.Context, q db.Querier, userID, userAgent, ip string) (token, sessionID string, err error) {
	sessionID = uuid.NewString()
	token, err = s.insertRefreshToken(ctx, q, sessionID, userID, userAgent, ip, db.Now())
	return token, sessionID, err
}

// insertRefreshToken adds a token to a session; started is the session's
// created_at, carried across rotations.
func (s *Service) insertRefreshToken(ctx context.Context, q db.Querier, sessionID, userID, userAgent, ip string, started db.Time) (string, error) {
	token := RandomToken(32)
	_, err := q.ExecContext(ctx, `INSERT INTO refresh_tokens (id, session_id, token_hash, user_id, expires_at, user_agent, ip_address, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), sessionID, HashToken(token), userID, db.T(time.Now().Add(s.Cfg.RefreshTokenTTL)), truncate(userAgent, 512), truncate(ip, 45), started, db.Now())
	return token, err
}

var ErrInvalidRefresh = errors.New("invalid or expired refresh token")

// rotationGrace is how long a token replaced by a refresh may be replayed
// without counting as theft. Tabs (or a waking laptop) refresh at once, and a
// refresh response can be lost after the server rotated (sleep, flaky
// network); a replay in this window gets the replacement token back.
const rotationGrace = 60 * time.Second

// maxReplacementHops bounds following replaced_by when the replacement was
// itself rotated within the grace window.
const maxReplacementHops = 5

// RotateRefreshToken validates token, revokes it and issues a replacement in
// the same session. Replaying a just-rotated token within rotationGrace
// returns its live replacement, so a refresh is safe to retry. Any other reuse
// of a revoked token revokes every token of that user (theft signal), except
// one whose session was revoked from Settings or that a replay can't follow to
// a live token, which only fail.
func (s *Service) RotateRefreshToken(ctx context.Context, token, userAgent, ip string) (*models.User, string, string, error) {
	var user *models.User
	var newToken, sessionID, reusedBy string
	err := s.DB.Tx(ctx, func(tx *sql.Tx) error {
		var id, userID string
		var expires, started db.Time
		var revoked db.NullTime
		var reason, replacedBy sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT id, session_id, user_id, expires_at, created_at, revoked_at, revoked_reason, replaced_by FROM refresh_tokens WHERE token_hash = ?`, HashToken(token)).
			Scan(&id, &sessionID, &userID, &expires, &started, &revoked, &reason, &replacedBy)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvalidRefresh
		}
		if err != nil {
			return err
		}
		if revoked.Valid {
			if reason.String == "rotated" && time.Since(revoked.Time.Time) <= rotationGrace {
				newToken, err = s.liveReplacement(ctx, tx, replacedBy.String)
				if err != nil {
					return err
				}
				user, err = activeUser(ctx, tx, userID)
				return err
			}
			if reason.String != "revoked" {
				reusedBy = userID
			}
			return ErrInvalidRefresh
		}
		if time.Now().After(expires.Time) {
			return ErrInvalidRefresh
		}
		if user, err = activeUser(ctx, tx, userID); err != nil {
			return err
		}
		newToken, err = s.insertRefreshToken(ctx, tx, sessionID, userID, userAgent, ip, started)
		if err != nil {
			return err
		}
		enc, err := s.Keys.Encrypt(newToken)
		if err != nil {
			return err
		}
		now := db.Now()
		_, err = tx.ExecContext(ctx, `UPDATE refresh_tokens SET revoked_at = ?, revoked_reason = 'rotated', replaced_by = ?, updated_at = ? WHERE id = ?`, now, enc, now, id)
		return err
	})
	if reusedBy != "" {
		// Outside the tx: returning the error rolled it back, and the
		// theft-triggered revoke-all must persist.
		if rerr := s.RevokeAllRefreshTokens(ctx, s.DB, reusedBy); rerr != nil {
			return nil, "", "", rerr
		}
	}
	if err != nil {
		return nil, "", "", err
	}
	return user, newToken, sessionID, nil
}

// liveReplacement follows a rotated token's replaced_by to the session's live
// token. ErrInvalidRefresh when the chain ends anywhere else (logged out,
// revoked, expired, or rotated before replaced_by existed).
func (s *Service) liveReplacement(ctx context.Context, tx *sql.Tx, replacedBy string) (string, error) {
	for range maxReplacementHops {
		if replacedBy == "" {
			return "", ErrInvalidRefresh
		}
		token, err := s.Keys.Decrypt(replacedBy)
		if err != nil {
			return "", ErrInvalidRefresh
		}
		var expires db.Time
		var revoked db.NullTime
		var reason, next sql.NullString
		err = tx.QueryRowContext(ctx, `SELECT expires_at, revoked_at, revoked_reason, replaced_by FROM refresh_tokens WHERE token_hash = ?`, HashToken(token)).
			Scan(&expires, &revoked, &reason, &next)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvalidRefresh
		}
		if err != nil {
			return "", err
		}
		if !revoked.Valid {
			if time.Now().After(expires.Time) {
				return "", ErrInvalidRefresh
			}
			return token, nil
		}
		if reason.String != "rotated" {
			return "", ErrInvalidRefresh
		}
		replacedBy = next.String
	}
	return "", ErrInvalidRefresh
}

func activeUser(ctx context.Context, q db.Querier, userID string) (*models.User, error) {
	u := &models.User{}
	err := q.QueryRowContext(ctx, `SELECT `+models.UserColumns+` FROM users WHERE id = ?`, userID).Scan(u.ScanFields()...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidRefresh
	}
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		return nil, ErrInvalidRefresh
	}
	return u, nil
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
	// Past the grace window a replay can't use the replacement; don't keep it.
	_, _ = s.DB.ExecContext(ctx, `UPDATE refresh_tokens SET replaced_by = NULL WHERE replaced_by IS NOT NULL AND revoked_at < ?`, db.T(time.Now().Add(-rotationGrace)))
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
