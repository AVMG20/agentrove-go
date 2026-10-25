package profiles

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// Profile states shown in Settings.
const (
	StatusReady      = "ready"
	StatusSignIn     = "needs_sign_in"
	StatusCLIMissing = "cli_missing"
)

// Account is who a signed-in profile runs as, when the CLI recorded it.
type Account struct {
	Email string `json:"email,omitempty"`
	Plan  string `json:"plan,omitempty"`
}

// Status is a profile's readiness. It is read from the host copy of the box
// home, so it never wakes the box.
type Status struct {
	State   string   `json:"status"`
	Detail  string   `json:"status_detail"`
	Account *Account `json:"account"`
}

// Binary is the CLI a provider runs.
func Binary(provider string) string {
	switch provider {
	case models.ProviderCodex:
		return "codex"
	case models.ProviderAntigravity:
		return "agy"
	}
	return "claude"
}

// ProviderName is a provider's display name.
func ProviderName(provider string) string {
	switch provider {
	case models.ProviderCodex:
		return "Codex"
	case models.ProviderAntigravity:
		return "Antigravity"
	}
	return "Claude Code"
}

// claudeCredentialEnv are the env vars that sign the built-in profile in.
var claudeCredentialEnv = []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// Status reports whether p can run. env is the deployment + user env (the
// built-in profile signs in through it).
func (s *Service) Status(ctx context.Context, p *Profile, env map[string]string) Status {
	if s.probes.missing(p.UserID, Binary(p.Provider)) {
		return Status{State: StatusCLIMissing, Detail: "`" + Binary(p.Provider) + "` is not installed in your box"}
	}
	home := s.cfg.AgentHomeDir(p.UserID)
	rel := strings.TrimPrefix(p.ConfigDir(), sandbox.ContainerHome+"/")
	switch {
	case p.Builtin:
		for _, k := range claudeCredentialEnv {
			if strings.TrimSpace(env[k]) != "" {
				return Status{State: StatusReady, Detail: "Server credentials"}
			}
		}
		if hostFileExists(home, path.Join(rel, ".credentials.json")) {
			return Status{State: StatusReady, Account: claudeAccount(home, path.Join(rel, ".claude.json"))}
		}
		return Status{State: StatusSignIn, Detail: "No Claude credentials on the server or in your box"}
	case p.AuthMode == AuthToken || p.AuthMode == AuthEndpoint:
		if p.Token == "" {
			return Status{State: StatusSignIn, Detail: "No token yet"}
		}
		return Status{State: StatusReady}
	case p.Provider == models.ProviderCodex:
		if data, err := readHostFile(home, path.Join(rel, "auth.json")); err == nil {
			if acct, ok := codexAccount(data); ok {
				return Status{State: StatusReady, Account: acct}
			}
		}
		return Status{State: StatusSignIn, Detail: "Not signed in to Codex"}
	case p.Provider == models.ProviderAntigravity:
		if hostFileExists(home, path.Join(rel, ".gemini/antigravity-cli/antigravity-oauth-token")) {
			return Status{State: StatusReady, Account: geminiAccount(home, path.Join(rel, ".gemini/google_accounts.json"))}
		}
		return Status{State: StatusSignIn, Detail: "Not signed in to Antigravity"}
	}
	return Status{State: StatusSignIn}
}

// signedIn reports whether a login flow has produced credentials.
func (s *Service) signedIn(p *Profile) bool {
	return s.Status(context.Background(), p, nil).State == StatusReady
}

const maxCredentialFile = 256 << 10

// readHostFile reads a file under the user's box home through an os.Root:
// the box can plant symlinks there, which must not lead the API elsewhere.
func readHostFile(home, rel string) ([]byte, error) {
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return sandbox.ReadRegular(root, rel, maxCredentialFile)
}

func hostFileExists(home, rel string) bool {
	data, err := readHostFile(home, rel)
	return err == nil && len(strings.TrimSpace(string(data))) > 0
}

// claudeAccount reads the signed-in account from .claude.json (oauthAccount).
func claudeAccount(home, rel string) *Account {
	data, err := readHostFile(home, rel)
	if err != nil {
		return nil
	}
	var cfg struct {
		OAuthAccount *struct {
			EmailAddress     string `json:"emailAddress"`
			OrganizationType string `json:"organizationType"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &cfg) != nil || cfg.OAuthAccount == nil {
		return nil
	}
	return &Account{Email: cfg.OAuthAccount.EmailAddress, Plan: planName(strings.TrimPrefix(cfg.OAuthAccount.OrganizationType, "claude_"))}
}

// codexAccount reads auth.json: a ChatGPT login (tokens.id_token, a JWT with
// the email and plan) or a stored API key.
func codexAccount(data []byte) (*Account, bool) {
	var auth struct {
		APIKey *string `json:"OPENAI_API_KEY"`
		Tokens *struct {
			IDToken string `json:"id_token"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &auth) != nil {
		return nil, false
	}
	if auth.Tokens != nil && auth.Tokens.IDToken != "" {
		return jwtAccount(auth.Tokens.IDToken), true
	}
	return nil, auth.APIKey != nil && *auth.APIKey != ""
}

func jwtAccount(token string) *Account {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var claims struct {
		Email   string `json:"email"`
		Profile struct {
			Email string `json:"email"`
		} `json:"https://api.openai.com/profile"`
		Auth struct {
			Plan string `json:"chatgpt_plan_type"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return nil
	}
	email := claims.Email
	if email == "" {
		email = claims.Profile.Email
	}
	if email == "" && claims.Auth.Plan == "" {
		return nil
	}
	return &Account{Email: email, Plan: planName(claims.Auth.Plan)}
}

// geminiAccount reads the active Google account ({"active": "<email>"}).
func geminiAccount(home, rel string) *Account {
	data, err := readHostFile(home, rel)
	if err != nil {
		return nil
	}
	var accts struct {
		Active string `json:"active"`
	}
	if json.Unmarshal(data, &accts) != nil || accts.Active == "" {
		return nil
	}
	return &Account{Email: accts.Active}
}

func planName(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "_", " "))
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// probes remembers which CLIs a user's box lacks. It is filled when the box
// runs anyway (a sign-in, a turn that found no CLI, or Settings while the box
// is up), so checking never starts a stopped box.
type probes struct {
	mu      sync.Mutex
	absent  map[string]map[string]bool // user -> binary -> missing
	checked map[string]time.Time
}

func newProbes() *probes {
	return &probes{absent: map[string]map[string]bool{}, checked: map[string]time.Time{}}
}

func (p *probes) missing(userID, bin string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.absent[userID][bin]
}

func (p *probes) set(userID string, absent map[string]bool) {
	p.mu.Lock()
	p.absent[userID] = absent
	p.checked[userID] = time.Now()
	p.mu.Unlock()
}

func (p *probes) fresh(userID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Since(p.checked[userID]) < 5*time.Minute
}

// NoteMissing records that a turn found bin missing in the user's box.
func (s *Service) NoteMissing(userID, bin string) {
	s.probes.mu.Lock()
	if s.probes.absent[userID] == nil {
		s.probes.absent[userID] = map[string]bool{}
	}
	s.probes.absent[userID][bin] = true
	s.probes.mu.Unlock()
}

// ProbeCLIs checks which provider CLIs the user's box has, if the box is
// running and the last check is stale.
func (s *Service) ProbeCLIs(ctx context.Context, userID string) {
	if s.probes.fresh(userID) || s.sandboxes == nil {
		return
	}
	sb, err := s.sandboxes.ForUser(userID)
	if err != nil || !sb.IsRunning(ctx) {
		return
	}
	bins := []string{"claude", "codex", "agy"}
	script := ""
	for _, b := range bins {
		script += "command -v " + b + " >/dev/null 2>&1 && echo " + b + "; "
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"bash", "-lc", script}})
	if err != nil {
		return
	}
	found := map[string]bool{}
	for _, line := range strings.Fields(res.Stdout) {
		found[line] = true
	}
	missing := map[string]bool{}
	for _, b := range bins {
		missing[b] = !found[b]
	}
	s.probes.set(userID, missing)
}
