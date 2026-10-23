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
	// HomeDir overrides HOME when set.
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
	env := os.Environ()
	if o.ConfigDir != "" {
		env = append(env, "CLAUDE_CONFIG_DIR="+o.ConfigDir)
	}
	if o.HomeDir != "" {
		env = append(env, "HOME="+o.HomeDir)
	}
	for k, v := range o.Env {
		env = append(env, k+"="+v)
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
