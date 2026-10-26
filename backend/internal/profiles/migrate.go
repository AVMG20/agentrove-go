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

// MigrateEnvVars moves each user's Claude env vars (from before profiles)
// into a Claude profile, made the default if the user had none, with all the
// user's chats moved onto it. The profile reproduces what the CLI ran with:
// the deployment's env (AGENT_ENV) overlaid with the user's vars. A user
// whose own vars don't decide the credential keeps every var as it was; vars
// the profile didn't take are kept too. It runs at
// startup, once per user (user_settings.profiles_env_migrated).
func (s *Service) MigrateEnvVars(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, custom_env_vars, default_profile_id FROM user_settings
		WHERE profiles_env_migrated = 0 AND custom_env_vars IS NOT NULL AND custom_env_vars != ''`)
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
	global := map[string]string{}
	if s.cfg != nil {
		global = s.cfg.AgentGlobalEnv()
	}
	for _, p := range todo {
		if err := s.migrateUser(ctx, p.userID, global, p.vars, p.hasDefault); err != nil {
			slog.Warn("profiles: migrate Claude env vars", "user", p.userID, "err", err)
		}
	}
}

// claudeCredentialPrecedence is the order Claude Code picks a credential from
// its env in: a bearer token, then an API key, then an OAuth token.
var claudeCredentialPrecedence = []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN"}

const officialAPI = "https://api.anthropic.com"

// envMigration is what a user's env vars become: a profile (nil: none) and
// the user vars it took over, which leave the env vars.
type envMigration struct {
	profile *Profile
	taken   map[string]bool
}

// planEnvMigration works out the profile the user's env setup amounted to,
// from the env the CLI saw (global overlaid with the user's vars). There is
// one only when the credential the CLI used is the user's own and a profile
// can hold the rest (an http(s) base URL, no Bedrock/Vertex).
func planEnvMigration(userID string, global map[string]string, vars []models.CustomEnvVar) envMigration {
	user := map[string]string{}
	for _, v := range vars {
		if k := strings.TrimSpace(v.Key); k != "" {
			user[k] = strings.TrimSpace(v.Value)
		}
	}
	eff := map[string]string{}
	for k, v := range global {
		eff[k] = strings.TrimSpace(v)
	}
	for k, v := range user {
		eff[k] = v
	}
	none := envMigration{}
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"} {
		if v := eff[k]; v != "" && v != "0" && !strings.EqualFold(v, "false") {
			return none
		}
	}
	tokenEnv := ""
	for _, k := range claudeCredentialPrecedence {
		if eff[k] != "" {
			tokenEnv = k
			break
		}
	}
	if tokenEnv == "" || user[tokenEnv] == "" {
		// No credential, or the deployment's wins: the user's vars didn't
		// choose the account.
		return none
	}
	_, userBase := user["ANTHROPIC_BASE_URL"]
	_, userUsage := user["CLAUDE_USAGE_URL"]
	// ~/claude, where threads from before accounts ran: chats moved onto
	// this profile keep resuming the sessions stored there.
	p := &Profile{UserID: userID, Provider: models.ProviderClaude, Name: "Claude (from env vars)", configDir: "claude",
		AuthMode: AuthToken, Token: eff[tokenEnv], TokenEnv: tokenEnv}
	if len(p.Token) > maxTokenLen || strings.ContainsAny(p.Token, " \t\r\n") {
		return none
	}
	taken := map[string]bool{tokenEnv: true}
	base := strings.TrimRight(eff["ANTHROPIC_BASE_URL"], "/")
	if base != "" && base != officialAPI {
		if validURL(base, "", true) != nil {
			return none // a profile can't hold it; keep running as before
		}
		p.AuthMode, p.BaseURL = AuthEndpoint, base
		if userBase {
			taken["ANTHROPIC_BASE_URL"] = true
		}
		if u := eff["CLAUDE_USAGE_URL"]; u != "" && validURL(u, "", false) == nil {
			p.UsageURL = u
			if userUsage {
				taken["CLAUDE_USAGE_URL"] = true
			}
		}
	}
	return envMigration{profile: p, taken: taken}
}

func (s *Service) migrateUser(ctx context.Context, userID string, global map[string]string, vars []models.CustomEnvVar, hasDefault bool) error {
	plan := planEnvMigration(userID, global, vars)
	kept := make([]models.CustomEnvVar, 0, len(vars))
	for _, v := range vars {
		if !plan.taken[strings.TrimSpace(v.Key)] {
			kept = append(kept, v)
		}
	}
	p := plan.profile
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		if p != nil {
			enc, err := s.sealToken(p.Token)
			if err != nil {
				return err
			}
			p.ID = uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO agent_profiles (`+profileColumns+`) VALUES (`+profilePlaceholders+`)`,
				p.ID, userID, p.Provider, p.Name, p.AuthMode, p.BaseURL, p.UsageURL, enc, p.TokenEnv, p.configDir, now, now); err != nil {
				return err
			}
			// The user's chats ran on these vars, channel members included:
			// keep them all on the same account.
			if _, err := tx.ExecContext(ctx, `UPDATE chats SET profile_id = ?, provider = ? WHERE user_id = ? AND profile_id IS NULL`,
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
		_, err := tx.ExecContext(ctx, `UPDATE user_settings SET custom_env_vars = ?, profiles_env_migrated = 1, updated_at = ? WHERE user_id = ?`, string(b), now, userID)
		if err == nil {
			slog.Info("profiles: looked at Claude env vars", "user", userID, "profile", p != nil, "moved_vars", len(plan.taken))
		}
		return err
	})
}

// MigrateServerLogin moves threads from before accounts (no profile: they
// ran on the box's ~/claude login) onto a Claude sign-in account on ~/claude,
// so they keep resuming. Only users whose ~/claude holds a login get one;
// other such threads couldn't run anyway and ask for a new thread. It runs at
// startup; once moved, nothing is left to move.
func (s *Service) MigrateServerLogin(ctx context.Context) {
	if s.cfg == nil {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT user_id FROM chats WHERE profile_id IS NULL AND deleted_at IS NULL`)
	if err != nil {
		slog.Warn("profiles: list threads without an account", "err", err)
		return
	}
	var users []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			users = append(users, id)
		}
	}
	rows.Close()
	for _, userID := range users {
		if !hostFileExists(s.cfg.AgentHomeDir(userID), "claude/.credentials.json") {
			continue
		}
		if err := s.migrateServerLogin(ctx, userID); err != nil {
			slog.Warn("profiles: move threads onto the box's Claude login", "user", userID, "err", err)
		}
	}
}

func (s *Service) migrateServerLogin(ctx context.Context, userID string) error {
	name := "Claude"
	var clash int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM agent_profiles WHERE user_id = ? AND provider = ? AND name = ? COLLATE NOCASE`,
		userID, models.ProviderClaude, name).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		name = "Claude (box login)"
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		now := db.Now()
		id := uuid.NewString()
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_profiles (`+profileColumns+`) VALUES (`+profilePlaceholders+`)`,
			id, userID, models.ProviderClaude, name, AuthLogin, "", "", "", "", "claude", now, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE chats SET profile_id = ?, provider = ? WHERE user_id = ? AND profile_id IS NULL`,
			id, models.ProviderClaude, userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE user_settings SET default_profile_id = ? WHERE user_id = ? AND (default_profile_id IS NULL OR default_profile_id = '')`, id, userID)
		if err == nil {
			slog.Info("profiles: moved threads onto the box's Claude login", "user", userID, "profile", id)
		}
		return err
	})
}
