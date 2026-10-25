// Package profiles manages provider profiles: named agent accounts a chat
// runs on (several Claude accounts, Codex, Antigravity). Each profile has its
// own auth in the user's box, under ~/.agentrove/profiles/<id>, so two
// accounts never share a login.
//
// The deployment's env keys (CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_*), the
// user's env vars and the box's ~/claude login form the built-in Claude
// profile. It has no row: chats without a profile run on it, and it is the
// default until the user picks another one.
package profiles

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
	"agentrove/internal/store"

	"github.com/google/uuid"
)

// BuiltinID is the API id of the built-in Claude profile.
const BuiltinID = "builtin"

// Auth modes. Claude profiles hold a token (an OAuth token from `claude
// setup-token`, or an API key) or a custom endpoint (base URL + token, plus
// an optional usage URL). Codex and Antigravity sign in with the CLI's own
// login in the box, or use an API key.
const (
	AuthToken    = "token"
	AuthEndpoint = "endpoint"
	AuthLogin    = "login"
)

type Profile struct {
	ID       string
	UserID   string
	Provider string
	Name     string
	AuthMode string
	BaseURL  string
	UsageURL string
	Token    string // plaintext; never sent to the client
	// configDir overrides the CLI config dir (relative to the box home).
	configDir string
	Builtin   bool
	CreatedAt db.Time
	UpdatedAt db.Time
}

// Builtin returns the built-in Claude profile of a user.
func Builtin(userID string) *Profile {
	return &Profile{ID: BuiltinID, UserID: userID, Provider: models.ProviderClaude, Name: "Default", AuthMode: AuthLogin, Builtin: true}
}

// Service stores profiles and runs their sign-in flows.
type Service struct {
	db        *db.DB
	keys      *auth.Keys
	cfg       *config.Config
	sandboxes *sandbox.Manager
	logins    *logins
	probes    *probes
}

func NewService(d *db.DB, keys *auth.Keys, cfg *config.Config, sandboxes *sandbox.Manager) *Service {
	return &Service{db: d, keys: keys, cfg: cfg, sandboxes: sandboxes, logins: newLogins(), probes: newProbes()}
}

const profileColumns = `id, user_id, provider, name, auth_mode, base_url, usage_url, token, config_dir, created_at, updated_at`

func (s *Service) scan(row interface{ Scan(...any) error }) (*Profile, error) {
	p := &Profile{}
	var enc string
	if err := row.Scan(&p.ID, &p.UserID, &p.Provider, &p.Name, &p.AuthMode, &p.BaseURL, &p.UsageURL, &enc, &p.configDir, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if enc != "" {
		// A token that no longer decrypts (SECRET_KEY changed) counts as unset
		// rather than being handed to the CLI as "enc:...".
		if plain, err := s.keys.Decrypt(enc); err == nil {
			p.Token = plain
		}
	}
	return p, nil
}

// List returns the user's profiles, the built-in one first.
func (s *Service) List(ctx context.Context, userID string) ([]*Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileColumns+` FROM agent_profiles WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Profile{Builtin(userID)}
	for rows.Next() {
		p, err := s.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Get returns one of the user's profiles; "" and BuiltinID are the built-in one.
func (s *Service) Get(ctx context.Context, userID, id string) (*Profile, error) {
	if id == "" || id == BuiltinID {
		return Builtin(userID), nil
	}
	p, err := s.scan(s.db.QueryRowContext(ctx, `SELECT `+profileColumns+` FROM agent_profiles WHERE id = ? AND user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Profile not found")
	}
	return p, err
}

// DefaultID is the id of the user's default profile.
func (s *Service) DefaultID(ctx context.Context, userID string) string {
	var id *string
	_ = s.db.QueryRowContext(ctx, `SELECT default_profile_id FROM user_settings WHERE user_id = ?`, userID).Scan(&id)
	if id == nil || *id == "" {
		return BuiltinID
	}
	return *id
}

// Default returns the user's default profile (the built-in one if the stored
// default is gone).
func (s *Service) Default(ctx context.Context, userID string) *Profile {
	if p, err := s.Get(ctx, userID, s.DefaultID(ctx, userID)); err == nil {
		return p
	}
	return Builtin(userID)
}

// DefaultClaude is the Claude profile for Claude-only work outside a chat's
// own profile (titles, commit messages, automations): the default profile
// when it is a Claude one, else the built-in.
func (s *Service) DefaultClaude(ctx context.Context, userID string) *Profile {
	if p := s.Default(ctx, userID); p.Provider == models.ProviderClaude {
		return p
	}
	return Builtin(userID)
}

// SetDefault makes id the user's default profile.
func (s *Service) SetDefault(ctx context.Context, userID, id string) error {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return err
	}
	var stored any
	if id != BuiltinID {
		stored = id
	}
	res, err := s.db.ExecContext(ctx, `UPDATE user_settings SET default_profile_id = ?, updated_at = ? WHERE user_id = ?`, stored, db.Now(), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		now := db.Now()
		_, err = s.db.ExecContext(ctx, `INSERT INTO user_settings (id, user_id, notifications_enabled, title_model_id, default_profile_id, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, ?, ?) ON CONFLICT(user_id) DO UPDATE SET default_profile_id = excluded.default_profile_id`,
			uuid.NewString(), userID, store.DefaultTitleModelID, stored, now, now)
	}
	return err
}

// Input is a create or update request; nil fields are left as they are.
type Input struct {
	Provider *string `json:"provider"`
	Name     *string `json:"name"`
	AuthMode *string `json:"auth_mode"`
	BaseURL  *string `json:"base_url"`
	UsageURL *string `json:"usage_url"`
	// Token: nil or "" keeps the stored one; ClearToken removes it.
	Token      *string `json:"token"`
	ClearToken bool    `json:"clear_token"`
}

const (
	maxNameLen  = 60
	maxTokenLen = 8 << 10
)

// AuthModes lists a provider's auth modes, the first being the default.
func AuthModes(provider string) []string {
	if provider == models.ProviderClaude {
		return []string{AuthToken, AuthEndpoint}
	}
	return []string{AuthLogin, AuthToken}
}

// apply validates in onto p (a new profile when p.ID is empty).
func (s *Service) apply(ctx context.Context, p *Profile, in Input) error {
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" || utf8.RuneCountInString(name) > maxNameLen {
			return httpx.Unprocessable("Name must be 1-60 characters")
		}
		p.Name = name
	}
	if in.AuthMode != nil {
		p.AuthMode = *in.AuthMode
	}
	modes := AuthModes(p.Provider)
	if p.AuthMode == "" {
		p.AuthMode = modes[0]
	}
	valid := false
	for _, m := range modes {
		valid = valid || m == p.AuthMode
	}
	if !valid {
		return httpx.Unprocessable("Unsupported sign-in method for this provider: " + p.AuthMode)
	}
	if in.BaseURL != nil {
		p.BaseURL = strings.TrimRight(strings.TrimSpace(*in.BaseURL), "/")
	}
	if in.UsageURL != nil {
		p.UsageURL = strings.TrimSpace(*in.UsageURL)
	}
	if p.AuthMode != AuthEndpoint {
		// Only a custom endpoint has URLs; drop leftovers of a mode switch.
		p.BaseURL, p.UsageURL = "", ""
	} else {
		if err := validURL(p.BaseURL, "Base URL", true); err != nil {
			return err
		}
		if err := validURL(p.UsageURL, "Usage URL", false); err != nil {
			return err
		}
	}
	if in.ClearToken {
		p.Token = ""
	}
	if in.Token != nil && strings.TrimSpace(*in.Token) != "" {
		tok := strings.TrimSpace(*in.Token)
		if len(tok) > maxTokenLen || strings.ContainsAny(tok, " \t\r\n") {
			return httpx.Unprocessable("The token must be a single line without spaces")
		}
		p.Token = tok
	}
	if p.AuthMode == AuthEndpoint && p.Token == "" {
		return httpx.Unprocessable("A custom endpoint needs a token")
	}
	if p.AuthMode == AuthLogin {
		p.Token = ""
	}
	var clash int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_profiles WHERE user_id = ? AND id != ? AND provider = ? AND name = ? COLLATE NOCASE`,
		p.UserID, p.ID, p.Provider, p.Name).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 || (p.Provider == models.ProviderClaude && strings.EqualFold(p.Name, Builtin(p.UserID).Name)) {
		return httpx.Conflict("A profile with that name already exists")
	}
	return nil
}

func validURL(raw, field string, required bool) error {
	if raw == "" {
		if required {
			return httpx.Unprocessable(field + " is required")
		}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(raw) > 2048 {
		return httpx.Unprocessable(field + " must be an http(s) URL")
	}
	return nil
}

func (s *Service) sealToken(tok string) (string, error) {
	if tok == "" {
		return "", nil
	}
	return s.keys.Encrypt(tok)
}

// Create adds a profile.
func (s *Service) Create(ctx context.Context, userID string, in Input) (*Profile, error) {
	if in.Provider == nil || !models.ValidProvider(*in.Provider) {
		return nil, httpx.Unprocessable("provider must be claude, codex or antigravity")
	}
	if in.Name == nil {
		return nil, httpx.Unprocessable("Name must be 1-60 characters")
	}
	p := &Profile{UserID: userID, Provider: *in.Provider}
	if err := s.apply(ctx, p, in); err != nil {
		return nil, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_profiles WHERE user_id = ?`, userID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= 50 {
		return nil, httpx.Unprocessable("You can have at most 50 profiles")
	}
	enc, err := s.sealToken(p.Token)
	if err != nil {
		return nil, err
	}
	p.ID, p.CreatedAt = uuid.NewString(), db.Now()
	p.UpdatedAt = p.CreatedAt
	_, err = s.db.ExecContext(ctx, `INSERT INTO agent_profiles (`+profileColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.UserID, p.Provider, p.Name, p.AuthMode, p.BaseURL, p.UsageURL, enc, p.configDir, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Update edits a profile; the built-in one can't be edited.
func (s *Service) Update(ctx context.Context, userID, id string, in Input) (*Profile, error) {
	p, err := s.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if p.Builtin {
		return nil, httpx.BadRequest("The built-in profile uses the server's settings and can't be edited")
	}
	if in.Provider != nil && *in.Provider != p.Provider {
		return nil, httpx.BadRequest("A profile's provider can't be changed")
	}
	if err := s.apply(ctx, p, in); err != nil {
		return nil, err
	}
	return p, s.save(ctx, p)
}

func (s *Service) save(ctx context.Context, p *Profile) error {
	enc, err := s.sealToken(p.Token)
	if err != nil {
		return err
	}
	p.UpdatedAt = db.Now()
	_, err = s.db.ExecContext(ctx, `UPDATE agent_profiles SET name = ?, auth_mode = ?, base_url = ?, usage_url = ?, token = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`, p.Name, p.AuthMode, p.BaseURL, p.UsageURL, enc, p.UpdatedAt, p.ID, p.UserID)
	return err
}

// Delete removes a profile. Its chats keep the id and say so when used; a
// deleted default falls back to the built-in profile. The login files in the
// box stay (the box is the user's own), so re-adding needs a new sign-in.
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	p, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if p.Builtin {
		return httpx.BadRequest("The built-in profile can't be deleted")
	}
	s.logins.cancel(p.ID)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_profiles WHERE id = ? AND user_id = ?`, p.ID, userID); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE user_settings SET default_profile_id = NULL WHERE user_id = ? AND default_profile_id = ?`, userID, p.ID)
	return err
}

// ForChat resolves the profile a chat runs on. A deleted profile is an
// error the user can act on (start a new thread), not a silent switch.
func (s *Service) ForChat(ctx context.Context, userID string, profileID *string) (*Profile, error) {
	p, err := s.Get(ctx, userID, db.Deref(profileID))
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == 404 {
			return nil, httpx.BadRequest("This thread's profile was deleted. Start a new thread to continue with another profile.")
		}
		return nil, err
	}
	return p, nil
}
