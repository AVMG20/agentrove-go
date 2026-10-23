package auth

import (
	"context"
	"net/http"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

type authConfig struct {
	RegistrationEnabled bool `json:"registration_enabled"`
	// Set when REGISTRATION_DISABLED pins it off; the admin toggle can't override it.
	RegistrationLocked bool `json:"registration_locked"`
}

func (s *Service) authConfig(ctx context.Context) authConfig {
	if s.Cfg.RegistrationDisabled {
		return authConfig{RegistrationLocked: true}
	}
	var v string
	err := s.DB.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = 'registration_enabled'`).Scan(&v)
	return authConfig{RegistrationEnabled: err != nil || v != "false"}
}

// needsFirstUser reports an instance without accounts: the first sign-up is
// always allowed so registration can't lock everyone out.
func (s *Service) needsFirstUser(ctx context.Context) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`).Scan(&n)
	return n == 0
}

// getAuthConfig is public: the login page uses it to hide sign-up.
func (s *Service) getAuthConfig(w http.ResponseWriter, r *http.Request) error {
	cfg := s.authConfig(r.Context())
	if !cfg.RegistrationEnabled && s.needsFirstUser(r.Context()) {
		cfg.RegistrationEnabled = true
	}
	return httpx.OK(w, cfg)
}

func (s *Service) setRegistration(w http.ResponseWriter, r *http.Request) error {
	if !UserFrom(r.Context()).IsSuperuser {
		return httpx.Forbidden("Only the administrator can change registration")
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := httpx.Decode(r, &body); err != nil {
		return err
	}
	v := "false"
	if body.Enabled {
		v = "true"
	}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO app_settings (key, value, updated_at) VALUES ('registration_enabled', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, v, db.Now()); err != nil {
		return err
	}
	return httpx.OK(w, s.authConfig(r.Context()))
}
