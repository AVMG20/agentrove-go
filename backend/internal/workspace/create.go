package workspace

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"agentrove/internal/httpx"

	"github.com/google/uuid"
)

const gitCloneTimeout = 180 * time.Second

func shortHex() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:8] }

// NewSandboxID is a short random id (also the container name suffix).
func NewSandboxID() string { return strings.ReplaceAll(uuid.NewString(), "-", "")[:12] }

// SafeDirName turns a user-facing name into a single path segment.
func SafeDirName(name string) string {
	var b strings.Builder
	for _, c := range name {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-.")
	if len(s) > 100 {
		s = strings.Trim(s[:100], "-.")
	}
	if s == "" {
		return "workspace"
	}
	return s
}

// NormalizeGitURL accepts git@ SSH URLs untouched and HTTPS URLs without
// embedded credentials (tokens go through GIT_ASKPASS instead).
func NormalizeGitURL(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return "", httpx.BadRequest("git_url is required for git workspace")
	}
	if strings.HasPrefix(c, "git@") {
		return c, nil
	}
	u, err := url.Parse(c)
	if err != nil || u.Scheme != "https" {
		return "", httpx.BadRequest("git_url must be an HTTPS or git@... SSH URL")
	}
	if u.User != nil {
		return "", httpx.BadRequest("git_url must not contain embedded credentials")
	}
	return c, nil
}

// RepoName derives a safe directory name from a clone URL.
func RepoName(gitURL string) string {
	n := strings.TrimRight(gitURL, "/")
	if strings.HasPrefix(n, "git@") {
		if _, after, ok := strings.Cut(n, ":"); ok {
			n = after
		}
	} else if u, err := url.Parse(n); err == nil {
		n = u.Path
	}
	if i := strings.LastIndex(n, "/"); i >= 0 {
		n = n[i+1:]
	}
	n = strings.TrimSuffix(n, ".git")
	var b strings.Builder
	for _, c := range n {
		if unicode.IsLetter(c) || unicode.IsDigit(c) || c == '-' || c == '_' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	if s := strings.Trim(b.String(), "-"); s != "" {
		return s
	}
	return "workspace"
}

// cloneRepo shallow-clones (all branches visible) into parent/<repo>-<hex>.
// A GitHub token is supplied through a throwaway GIT_ASKPASS script so it
// never appears in argv or the remote URL.
func cloneRepo(ctx context.Context, parent, gitURL, token string) (string, error) {
	dir := filepath.Join(parent, RepoName(gitURL)+"-"+shortHex())
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if token != "" && strings.HasPrefix(gitURL, "https://") {
		if u, err := url.Parse(gitURL); err == nil && (u.Hostname() == "github.com" || u.Hostname() == "www.github.com") {
			f, err := os.CreateTemp("", "git-askpass-*.sh")
			if err != nil {
				return "", err
			}
			script := "#!/bin/sh\ncase \"$1\" in\n*Username*) echo \"x-access-token\" ;;\n*Password*) echo \"$GIT_PASSWORD\" ;;\nesac\n"
			_, _ = f.WriteString(script)
			f.Close()
			defer os.Remove(f.Name())
			_ = os.Chmod(f.Name(), 0o700)
			env = append(env, "GIT_ASKPASS="+f.Name(), "GIT_PASSWORD="+token)
		}
	}
	cctx, cancel := context.WithTimeout(ctx, gitCloneTimeout)
	defer cancel()
	// --depth implies --single-branch; keep every branch visible to the
	// branch selector at depth 1.
	cmd := exec.CommandContext(cctx, "git", "clone", "--depth", "1", "--no-single-branch", gitURL, dir)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if cctx.Err() != nil {
		_ = os.RemoveAll(dir)
		return "", httpx.BadRequest("Git clone timed out")
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = "Failed to clone repository"
		}
		if token != "" {
			msg = strings.ReplaceAll(msg, token, "***")
		}
		return "", httpx.BadRequest(msg)
	}
	return dir, nil
}
