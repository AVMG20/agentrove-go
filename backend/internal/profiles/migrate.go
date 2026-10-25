package profiles

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"

	"agentrove/internal/db"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

// managedEnv are the env var names profiles own: Claude's credentials,
// endpoint and usage URL, and the CLIs' config dirs. Settings → Environment
// Variables refuses them, so a stray value can't override a profile.
var managedEnv = map[string]bool{
	"ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true, "CLAUDE_CODE_OAUTH_TOKEN": true,
	"ANTHROPIC_BASE_URL": true, "CLAUDE_USAGE_URL": true, "CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true,
}

// ManagedEnvKey reports whether key is set through a provider profile
// rather than an env var.
func ManagedEnvKey(key string) bool { return managedEnv[strings.ToUpper(strings.TrimSpace(key))] }

// MigrateEnvVars turns each user's Claude env vars (from before profiles)
// into a Claude profile, made the default if the user had none, and drops
// them from the env vars. It runs at startup and is a no-op once done:
// nobody can add those vars any more.
func (s *Service) MigrateEnvVars(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, custom_env_vars, default_profile_id FROM user_settings WHERE custom_env_vars IS NOT NULL AND custom_env_vars != ''`)
	if err != nil {
		slog.Warn("profiles: list env vars to migrate", "err", err)
		return
	}
	type pending struct {
		userID     string
		vars       []models.CustomEnvVar
		hasDefault bool
	}
	var todo []pending
	for rows.Next() {
		var userID string
		var raw sql.NullString
		var def *string
		if rows.Scan(&userID, &raw, &def) != nil || !raw.Valid {
			continue
		}
		var vars []models.CustomEnvVar
		if json.Unmarshal([]byte(raw.String), &vars) != nil {
			continue
		}
		for _, v := range vars {
			if ManagedEnvKey(v.Key) {
				todo = append(todo, pending{userID, vars, def != nil && *def != ""})
				break
			}
		}
	}
	rows.Close()
	for _, p := range todo {
		if err := s.migrateUser(ctx, p.userID, p.vars, p.hasDefault); err != nil {
			slog.Warn("profiles: migrate Claude env vars", "user", p.userID, "err", err)
		}
	}
}

// profileFromEnv builds the Claude profile an env-var setup described, or
// nil when it holds no credentials.
func profileFromEnv(userID string, vars []models.CustomEnvVar) *Profile {
	env := map[string]string{}
	for _, v := range vars {
		env[strings.ToUpper(strings.TrimSpace(v.Key))] = strings.TrimSpace(v.Value)
	}
	first := func(keys ...string) string {
		for _, k := range keys {
			if env[k] != "" {
				return env[k]
			}
		}
		return ""
	}
	// ~/claude, like the built-in profile: chats moved onto this profile keep
	// resuming the sessions stored there.
	p := &Profile{UserID: userID, Provider: models.ProviderClaude, Name: "Claude (from env vars)", configDir: "claude"}
	if base := strings.TrimRight(env["ANTHROPIC_BASE_URL"], "/"); base != "" && validURL(base, "", true) == nil {
		p.AuthMode, p.BaseURL = AuthEndpoint, base
		p.Token = first("ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN")
		if u := env["CLAUDE_USAGE_URL"]; validURL(u, "", false) == nil {
			p.UsageURL = u
		}
	} else {
		p.AuthMode = AuthToken
		p.Token = first("CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN")
	}
	if p.Token == "" {
		return nil
	}
	return p
}

func (s *Service) migrateUser(ctx context.Context, userID string, vars []models.CustomEnvVar, hasDefault bool) error {
	kept := make([]models.CustomEnvVar, 0, len(vars))
	for _, v := range vars {
		if !ManagedEnvKey(v.Key) {
			kept = append(kept, v)
		}
	}
	p := profileFromEnv(userID, vars)
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		if p != nil {
			enc, err := s.sealToken(p.Token)
			if err != nil {
				return err
			}
			p.ID = uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_profiles (`+profileColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				p.ID, userID, p.Provider, p.Name, p.AuthMode, p.BaseURL, p.UsageURL, enc, p.configDir, now, now); err != nil {
				return err
			}
			// The user's chats ran on these vars: keep them on the same account.
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET profile_id = ?, provider = ? WHERE user_id = ? AND profile_id IS NULL AND channel_id IS NULL`,
				p.ID, p.Provider, userID); err != nil {
				return err
			}
			if !hasDefault {
				if _, err := tx.ExecContext(ctx, `UPDATE user_settings SET default_profile_id = ? WHERE user_id = ?`, p.ID, userID); err != nil {
					return err
				}
			}
		}
		b, _ := json.Marshal(kept)
		_, err := tx.ExecContext(ctx, `UPDATE user_settings SET custom_env_vars = ?, updated_at = ? WHERE user_id = ?`, string(b), now, userID)
		if err == nil {
			slog.Info("profiles: moved Claude env vars into a profile", "user", userID, "profile", p != nil)
		}
		return err
	})
}
