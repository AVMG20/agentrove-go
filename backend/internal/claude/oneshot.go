package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// OneShot runs a single text-only prompt (titles, prompt enhancement, inline
// questions) on the API host with no tools, using the user's Claude login.
type OneShot struct {
	Binary string
	// ConfigDir is CLAUDE_CONFIG_DIR (the user's shared login).
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
	tmp, err := os.MkdirTemp("", "agentrove-oneshot-")
	if err == nil {
		defer os.RemoveAll(tmp)
		cmd.Dir = tmp
	}
	env := oneShotEnv(os.Environ(), o.Env)
	if o.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+o.ConfigDir)
	}
	if home := o.HomeDir; home != "" {
		env = append(env, "HOME="+home)
	} else if tmp != "" {
		env = append(env, "HOME="+tmp)
	}
	cmd.Env = env
	if o.AsUID > 0 && os.Geteuid() == 0 {
		if tmp != "" {
			_ = os.Chown(tmp, o.AsUID, o.AsUID)
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(o.AsUID), Gid: uint32(o.AsUID)}}
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
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
