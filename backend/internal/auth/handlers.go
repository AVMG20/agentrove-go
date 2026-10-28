package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/netip"
	"net/smtp"
	"strings"
	"sync"
	"time"
	"unicode"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
}

type userRead struct {
	ID                        string `json:"id"`
	Email                     string `json:"email"`
	Username                  string `json:"username"`
	IsActive                  bool   `json:"is_active"`
	IsSuperuser               bool   `json:"is_superuser"`
	IsVerified                bool   `json:"is_verified"`
	EmailVerificationRequired bool   `json:"email_verification_required"`
}

func (s *Service) userRead(u *models.User) userRead {
	return userRead{ID: u.ID, Email: u.Email, Username: u.Username, IsActive: u.IsActive, IsSuperuser: u.IsSuperuser,
		IsVerified: u.IsVerified, EmailVerificationRequired: s.Cfg.RequireEmailVerification}
}

func (s *Service) Routes(mux *http.ServeMux, prefix string) {
	limiter := newRateLimiter(s.Cfg.TrustedProxies, s.BoxSubnets)
	mux.HandleFunc("POST "+prefix+"/auth/jwt/login", httpx.Wrap(limiter.limit("login", 10, time.Minute, s.login)))
	mux.HandleFunc("POST "+prefix+"/auth/register", httpx.Wrap(limiter.limit("register", 5, time.Minute, s.register)))
	mux.HandleFunc("POST "+prefix+"/auth/jwt/refresh", httpx.Wrap(limiter.limit("refresh", 30, time.Minute, s.refresh)))
	mux.HandleFunc("POST "+prefix+"/auth/jwt/logout", httpx.Wrap(s.logout))
	mux.HandleFunc("GET "+prefix+"/auth/me", s.Require(s.me))
	mux.HandleFunc("GET "+prefix+"/auth/sessions", s.Require(s.listSessions))
	mux.HandleFunc("DELETE "+prefix+"/auth/sessions", s.Require(s.revokeOtherSessions))
	mux.HandleFunc("DELETE "+prefix+"/auth/sessions/{id}", s.Require(s.revokeSession))
	mux.HandleFunc("GET "+prefix+"/auth/config", httpx.Wrap(s.getAuthConfig))
	mux.HandleFunc("PUT "+prefix+"/auth/registration", s.Require(s.setRegistration))
	mux.HandleFunc("POST "+prefix+"/auth/forgot-password", httpx.Wrap(limiter.limit("forgot", 5, time.Minute, s.forgotPassword)))
	mux.HandleFunc("POST "+prefix+"/auth/reset-password", httpx.Wrap(s.resetPassword))
	mux.HandleFunc("POST "+prefix+"/auth/request-verify-token", httpx.Wrap(limiter.limit("verify", 5, time.Minute, s.requestVerify)))
	mux.HandleFunc("POST "+prefix+"/auth/verify", httpx.Wrap(s.verify))
}

func (s *Service) login(w http.ResponseWriter, r *http.Request) error {
	if err := httpx.ParseForm(r, 1<<20); err != nil {
		return err
	}
	u, err := s.GetUserByEmail(r.Context(), r.FormValue("username"))
	if err != nil || !CheckPassword(u.HashedPassword, r.FormValue("password")) {
		return httpx.BadRequest("Invalid email or password")
	}
	if !u.IsActive {
		return httpx.BadRequest("Account is inactive")
	}
	if s.Cfg.RequireEmailVerification && !u.IsVerified {
		return httpx.BadRequest("Please verify your email before logging in")
	}
	return s.issueTokens(w, r, u)
}

func (s *Service) issueTokens(w http.ResponseWriter, r *http.Request, u *models.User) error {
	refresh, sessionID, err := s.CreateRefreshToken(r.Context(), s.DB, u.ID, r.UserAgent(), httpx.ClientIP(r, s.Cfg.TrustedProxies, s.BoxSubnets))
	if err != nil {
		return err
	}
	access, err := s.issueSessionToken(u.ID, sessionID)
	if err != nil {
		return err
	}
	return httpx.OK(w, tokenResponse{AccessToken: access, RefreshToken: refresh, TokenType: "bearer"})
}

func (s *Service) register(w http.ResponseWriter, r *http.Request) error {
	if !s.authConfig(r.Context()).RegistrationEnabled && !s.needsFirstUser(r.Context()) {
		return httpx.Forbidden("Registration is disabled")
	}
	var body struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	body.Email = strings.TrimSpace(body.Email)
	if _, err := mail.ParseAddress(body.Email); err != nil {
		return httpx.Unprocessable("Invalid email address")
	}
	if err := validateUsername(body.Username); err != nil {
		return httpx.Unprocessable(err.Error())
	}
	if len(body.Password) < 8 {
		return httpx.Unprocessable("Password must be at least 8 characters long")
	}
	hash, err := HashPassword(body.Password)
	if err != nil {
		return err
	}
	now := db.Now()
	u := &models.User{ID: uuid.NewString(), Email: body.Email, Username: body.Username, HashedPassword: hash,
		IsActive: true, IsVerified: !s.Cfg.RequireEmailVerification, CreatedAt: now, UpdatedAt: now}
	err = s.DB.Tx(r.Context(), func(tx *sql.Tx) error {
		// The first account administers the instance.
		var count int
		if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(1) FROM users`).Scan(&count); err != nil {
			return err
		}
		u.IsSuperuser = count == 0
		_, err := tx.ExecContext(r.Context(), `INSERT INTO users (id, email, username, hashed_password, is_active, is_superuser, is_verified, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, u.ID, u.Email, u.Username, u.HashedPassword, u.IsActive, u.IsSuperuser, u.IsVerified, now, now)
		return err
	})
	if err != nil {
		if db.IsUniqueViolation(err) {
			if strings.Contains(err.Error(), "username") {
				return httpx.BadRequest("Username already registered")
			}
			return httpx.BadRequest("Email already registered")
		}
		return err
	}
	if s.Cfg.RequireEmailVerification {
		s.sendVerification(r.Context(), u)
	}
	return httpx.Created(w, s.userRead(u))
}

func validateUsername(v string) error {
	if len(v) < 3 {
		return fmt.Errorf("Username must be at least 3 characters long")
	}
	if len(v) > 30 {
		return fmt.Errorf("Username must be less than 30 characters long")
	}
	for _, c := range v {
		if !(unicode.IsLetter(c) || unicode.IsDigit(c) || c == '_') {
			return fmt.Errorf("Username can only contain letters, numbers, and underscores")
		}
	}
	if strings.HasPrefix(v, "_") || strings.HasSuffix(v, "_") {
		return fmt.Errorf("Username cannot start or end with underscore")
	}
	return nil
}

func (s *Service) me(w http.ResponseWriter, r *http.Request) error {
	u := UserFrom(r.Context())
	return httpx.OK(w, map[string]any{"id": u.ID, "email": u.Email, "username": u.Username, "is_verified": u.IsVerified, "is_superuser": u.IsSuperuser})
}

func (s *Service) refresh(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	u, newRefresh, sessionID, err := s.RotateRefreshToken(r.Context(), body.RefreshToken, r.UserAgent(), httpx.ClientIP(r, s.Cfg.TrustedProxies, s.BoxSubnets))
	if errors.Is(err, ErrInvalidRefresh) {
		return httpx.Unauthorized("Invalid or expired refresh token")
	}
	// Anything else (e.g. a database error) is a 500: the client keeps its
	// session and retries, where a 401 would sign it out.
	if err != nil {
		return err
	}
	access, err := s.issueSessionToken(u.ID, sessionID)
	if err != nil {
		return err
	}
	return httpx.OK(w, tokenResponse{AccessToken: access, RefreshToken: newRefresh, TokenType: "bearer"})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if err := s.RevokeRefreshToken(r.Context(), body.RefreshToken); err != nil {
		return err
	}
	return httpx.NoContent(w)
}

func (s *Service) forgotPassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	// Always 202 so the endpoint can't be used to probe for accounts.
	if u, err := s.GetUserByEmail(r.Context(), body.Email); err == nil && u.IsActive {
		token := RandomToken(32)
		now := db.Now()
		_, err := s.DB.ExecContext(r.Context(), `UPDATE users SET reset_token = ?, reset_token_expires = ?, updated_at = ? WHERE id = ?`,
			HashToken(token), db.T(time.Now().Add(time.Hour)), now, u.ID)
		if err == nil {
			link := fmt.Sprintf("%s/reset-password?token=%s", strings.TrimRight(s.Cfg.FrontendURL, "/"), token)
			s.sendMail(u.Email, "Reset your Bitbox password", "Reset your password: "+link+"\n\nThis link expires in one hour.")
		}
	}
	return httpx.JSON(w, http.StatusAccepted, nil)
}

func (s *Service) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if len(body.Password) < 8 {
		return httpx.BadRequest("Password must be at least 8 characters long")
	}
	hash, err := HashPassword(body.Password)
	if err != nil {
		return err
	}
	ok, err := s.ResetPassword(r.Context(), body.Token, hash)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.BadRequest("RESET_PASSWORD_BAD_TOKEN")
	}
	return httpx.OK(w, nil)
}

func (s *Service) requestVerify(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email string `json:"email"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	if u, err := s.GetUserByEmail(r.Context(), body.Email); err == nil && !u.IsVerified {
		s.sendVerification(r.Context(), u)
	}
	return httpx.JSON(w, http.StatusAccepted, nil)
}

func (s *Service) sendVerification(ctx context.Context, u *models.User) {
	token := RandomToken(32)
	now := db.Now()
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET verification_token = ?, verification_token_expires = ?, updated_at = ? WHERE id = ?`,
		HashToken(token), db.T(time.Now().Add(24*time.Hour)), now, u.ID); err != nil {
		return
	}
	link := fmt.Sprintf("%s/verify-email?token=%s", strings.TrimRight(s.Cfg.FrontendURL, "/"), token)
	s.sendMail(u.Email, "Verify your Bitbox account", "Verify your email: "+link)
}

func (s *Service) verify(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	now := db.Now()
	var id string
	err := s.DB.QueryRowContext(r.Context(), `SELECT id FROM users WHERE verification_token = ? AND verification_token_expires > ?`, HashToken(body.Token), now).Scan(&id)
	if err != nil {
		return httpx.BadRequest("VERIFY_USER_BAD_TOKEN")
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE users SET is_verified = 1, verification_token = NULL, verification_token_expires = NULL, updated_at = ? WHERE id = ?`, now, id); err != nil {
		return err
	}
	u, err := s.GetUser(r.Context(), id)
	if err != nil {
		return err
	}
	return httpx.OK(w, s.userRead(u))
}

func (s *Service) sendMail(to, subject, body string) {
	if s.Cfg.MailPassword == "" {
		slog.Info("mail not configured; message logged instead", "to", to, "subject", subject, "body", body)
		return
	}
	go func() {
		from := fmt.Sprintf("%s <%s>", s.Cfg.MailFromName, s.Cfg.MailFrom)
		msg := "From: " + from + "\r\nTo: " + to + "\r\nSubject: " + subject + "\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + body
		addr := fmt.Sprintf("%s:%d", s.Cfg.MailServer, s.Cfg.MailPort)
		a := smtp.PlainAuth("", s.Cfg.MailUsername, s.Cfg.MailPassword, s.Cfg.MailServer)
		if err := smtp.SendMail(addr, a, s.Cfg.MailFrom, []string{to}, []byte(msg)); err != nil {
			slog.Error("send mail failed", "to", to, "err", err)
		}
	}()
}

// rateLimiter is a fixed-window per-IP limiter for the unauthenticated endpoints.
type rateLimiter struct {
	trusted   []netip.Prefix
	untrusted *httpx.PrefixSet
	mu        sync.Mutex
	counts    map[string]*window
}

type window struct {
	start time.Time
	n     int
}

func newRateLimiter(trusted []netip.Prefix, untrusted *httpx.PrefixSet) *rateLimiter {
	return &rateLimiter{trusted: trusted, untrusted: untrusted, counts: map[string]*window{}}
}

func (l *rateLimiter) limit(name string, max int, per time.Duration, h httpx.HandlerFunc) httpx.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		key := name + "|" + httpx.ClientIP(r, l.trusted, l.untrusted)
		now := time.Now()
		l.mu.Lock()
		win := l.counts[key]
		if win == nil || now.Sub(win.start) > per {
			win = &window{start: now}
			l.counts[key] = win
		}
		win.n++
		over := win.n > max
		if len(l.counts) > 10000 {
			for k, v := range l.counts {
				if now.Sub(v.start) > per {
					delete(l.counts, k)
				}
			}
		}
		l.mu.Unlock()
		if over {
			return &httpx.Error{Status: http.StatusTooManyRequests, Detail: "Too many requests"}
		}
		return h(w, r)
	}
}
