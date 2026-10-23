package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"agentrove/internal/sandbox"
)

// OneShot runs a single text-only prompt (titles, prompt enhancement, inline
// questions) on the API host with no tools, using the user's Claude login.
type OneShot struct {
	Binary string
	// ConfigDir is the user's Claude config dir (the box's ~/claude, holding
	// the login). The CLI never runs against it: it is box-writable, and the
	// CLI reads and writes many files there following symlinks. Run copies the
	// login into a private dir instead (see stageLogin).
	ConfigDir string
	// HomeDir overrides HOME; unset, HOME is the run's temp dir (never the
	// box's home, whose dotfiles the agent controls).
	HomeDir string
	// AsUID runs the CLI as this uid/gid (so files it writes in the shared
	// config dir stay owned by the sandbox user); 0 keeps the current user.
	AsUID int
	Env   map[string]string
}

func (o OneShot) Run(ctx context.Context, model, systemPrompt, prompt string) (string, error) {
	bin := o.Binary
	if bin == "" {
		bin = "claude"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	args := []string{"-p", "--output-format", "json", "--model", model, "--tools", "", "--no-session-persistence",
		// No setting sources: the config dir is the box's ~/claude, which the
		// agent can write, and settings there (hooks, apiKeyHelper, env) would
		// run on the API host. The login (.credentials.json) still loads.
		"--setting-sources", "", "--strict-mcp-config", "--disable-slash-commands"}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	// A private run dir is required: it holds the CLI's config dir.
	tmp, err := os.MkdirTemp("", "agentrove-oneshot-")
	if err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	defer os.RemoveAll(tmp)
	cmd.Dir = tmp
	cfgDir := filepath.Join(tmp, "config")
	if err := os.Mkdir(cfgDir, 0o700); err != nil {
		return "", fmt.Errorf("claude: %w", err)
	}
	var login *stagedLogin
	if o.ConfigDir != "" && !envHasCredentials(o.Env) {
		if login, err = stageLogin(o.ConfigDir, cfgDir); err != nil {
			return "", fmt.Errorf("claude: %w", err)
		}
	}
	env := oneShotEnv(os.Environ(), o.Env)
	env = append(env, "CLAUDE_CONFIG_DIR="+cfgDir)
	if home := o.HomeDir; home != "" {
		env = append(env, "HOME="+home)
	} else {
		env = append(env, "HOME="+tmp)
	}
	cmd.Env = env
	if o.AsUID > 0 && os.Geteuid() == 0 {
		// The run dir is private to this run; hand it (and the staged login)
		// to the CLI's uid.
		_ = filepath.WalkDir(tmp, func(p string, _ fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Lchown(p, o.AsUID, o.AsUID)
			}
			return nil
		})
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(o.AsUID), Gid: uint32(o.AsUID)}}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	// An OAuth refresh during the run rotates the refresh token; carry it
	// back or the box's login would be revoked. Done even if the run failed.
	if login != nil {
		if err := login.writeBack(cfgDir); err != nil {
			slog.Warn("one-shot: write back refreshed Claude login", "err", err)
		}
	}
	if err := runErr; err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		var r struct {
			Result string `json:"result"`
		}
		if json.Unmarshal(stdout.Bytes(), &r) == nil && r.Result != "" {
			msg = r.Result
		}
		return "", fmt.Errorf("claude: %s", firstLine(msg, err))
	}
	var r struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		return "", fmt.Errorf("claude: unexpected output")
	}
	if r.IsError {
		return "", fmt.Errorf("claude: %s", r.Result)
	}
	return strings.TrimSpace(r.Result), nil
}

func firstLine(s string, fallback error) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback.Error()
	}
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i]
	}
	return s
}

// ConfigDirFor returns the shared Claude config dir of a user in web mode.
func ConfigDirFor(agentHome string) string { return filepath.Join(agentHome, "claude") }

const (
	credentialsFile  = ".credentials.json"
	globalConfigFile = ".claude.json"
	maxCredentials   = 1 << 20
	maxGlobalConfig  = 16 << 20
)

// globalConfigKeys are the only .claude.json keys copied into a one-shot's
// private config: the account identity and API-key approvals the login needs.
// The rest (MCP servers, per-project state, ...) is agent-written and unused.
var globalConfigKeys = []string{"oauthAccount", "userID", "hasCompletedOnboarding", "primaryApiKey", "customApiKeyResponses"}

// envHasCredentials reports provider credentials in the env, in which case
// the one-shot needs no login files at all.
func envHasCredentials(env map[string]string) bool {
	for _, k := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"} {
		if env[k] != "" {
			return true
		}
	}
	return false
}

// stagedLogin remembers what was copied from the user's config dir, for the
// write-back after the run.
type stagedLogin struct {
	configDir string
	creds     []byte // .credentials.json as read; nil when there was none
	uid       int    // its owner (default: the sandbox user), kept on write-back
}

// openConfigParent opens the parent of configDir (the user's HOME mount, which
// the box cannot replace) and checks configDir's own entry is a real directory.
// The agent can swap ~/claude or files in it for symlinks; os.Root refuses any
// that lead out of HOME, and the Lstat checks refuse symlinks outright.
func openConfigParent(configDir string) (*os.Root, string, error) {
	root, err := os.OpenRoot(filepath.Dir(configDir))
	if err != nil {
		return nil, "", err
	}
	base := filepath.Base(configDir)
	fi, err := root.Lstat(base)
	if err != nil {
		root.Close()
		return nil, "", err
	}
	if !fi.IsDir() {
		root.Close()
		return nil, "", fmt.Errorf("the Claude config dir is not a directory (symlink?); refusing to read the login")
	}
	return root, base, nil
}

// stageLogin copies the login files the CLI needs from configDir into the
// private dir dst: .credentials.json verbatim and a .claude.json reduced to
// globalConfigKeys. A missing config dir or file is not an error (the CLI then
// reports it isn't logged in); a symlinked one is skipped.
func stageLogin(configDir, dst string) (*stagedLogin, error) {
	root, base, err := openConfigParent(configDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	login := &stagedLogin{configDir: configDir, uid: sandbox.ContainerUID}
	credPath := path.Join(base, credentialsFile)
	if creds, err := sandbox.ReadRegular(root, credPath, maxCredentials); err == nil {
		if err := os.WriteFile(filepath.Join(dst, credentialsFile), creds, 0o600); err != nil {
			return nil, err
		}
		login.creds = creds
		if fi, err := root.Lstat(credPath); err == nil {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				login.uid = int(st.Uid)
			}
		}
	}
	if raw, err := sandbox.ReadRegular(root, path.Join(base, globalConfigFile), maxGlobalConfig); err == nil {
		var all map[string]json.RawMessage
		if json.Unmarshal(raw, &all) == nil {
			keep := map[string]json.RawMessage{}
			for _, k := range globalConfigKeys {
				if v, ok := all[k]; ok {
					keep[k] = v
				}
			}
			if b, err := json.Marshal(keep); err == nil {
				if err := os.WriteFile(filepath.Join(dst, globalConfigFile), b, 0o600); err != nil {
					return nil, err
				}
			}
		}
	}
	return login, nil
}

// writeBack copies a .credentials.json the CLI rewrote (OAuth refresh) back to
// the user's config dir, atomically and without following symlinks, and only
// if the box's copy is still the one we read — a refresh the box did in the
// meantime wins. A login that wasn't there before is never created.
func (l *stagedLogin) writeBack(cfgDir string) error {
	if l.creds == nil {
		return nil
	}
	priv, err := os.OpenRoot(cfgDir)
	if err != nil {
		return err
	}
	defer priv.Close()
	updated, err := sandbox.ReadRegular(priv, credentialsFile, maxCredentials)
	if err != nil || bytes.Equal(updated, l.creds) || !json.Valid(updated) {
		return nil
	}
	root, base, err := openConfigParent(l.configDir)
	if err != nil {
		return err
	}
	defer root.Close()
	credPath := path.Join(base, credentialsFile)
	cur, err := sandbox.ReadRegular(root, credPath, maxCredentials)
	if err != nil || !bytes.Equal(cur, l.creds) {
		return nil
	}
	return sandbox.WriteFileAtomic(root, credPath, updated, 0o600, l.uid)
}

// hostEnvKeys are the API host's own vars a one-shot keeps (locale, proxy,
// CA bundle). Everything else (SECRET_KEY, MAIL_PASSWORD, ...) stays out.
var hostEnvKeys = map[string]bool{
	"PATH": true, "LANG": true, "LC_ALL": true, "TZ": true, "TMPDIR": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "NO_PROXY": true, "http_proxy": true, "https_proxy": true, "no_proxy": true,
	"NODE_EXTRA_CA_CERTS": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
}

// providerEnvKeys are the agent/user vars that choose the model provider and
// credentials. User-set vars like LD_PRELOAD, NODE_OPTIONS or PATH would run
// code on the API host, so only these pass.
var providerEnvKeys = map[string]bool{
	"CLAUDE_CODE_OAUTH_TOKEN": true, "CLAUDE_CODE_USE_BEDROCK": true, "CLAUDE_CODE_USE_VERTEX": true,
	"CLAUDE_CODE_SKIP_BEDROCK_AUTH": true, "CLAUDE_CODE_SKIP_VERTEX_AUTH": true, "CLAUDE_CODE_MAX_OUTPUT_TOKENS": true,
	"AWS_ACCESS_KEY_ID": true, "AWS_SECRET_ACCESS_KEY": true, "AWS_SESSION_TOKEN": true, "AWS_REGION": true,
	"AWS_DEFAULT_REGION": true, "AWS_BEARER_TOKEN_BEDROCK": true, "CLOUD_ML_REGION": true, "API_TIMEOUT_MS": true,
}

func providerEnvKey(k string) bool {
	return providerEnvKeys[k] || strings.HasPrefix(k, "ANTHROPIC_") || strings.HasPrefix(k, "VERTEX_REGION_")
}

// oneShotEnv is the env of a host-side one-shot: allowlisted host vars plus
// the provider vars of extra.
func oneShotEnv(host []string, extra map[string]string) []string {
	var env []string
	for _, kv := range host {
		if k, _, ok := strings.Cut(kv, "="); ok && hostEnvKeys[k] {
			env = append(env, kv)
		}
	}
	for k, v := range extra {
		if providerEnvKey(k) {
			env = append(env, k+"="+v)
		}
	}
	return env
}
