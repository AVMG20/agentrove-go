// Package sandbox runs commands and agent processes for a workspace, either in
// the user's Docker box or directly on the host.
//
// Each user gets one Docker "box": a container that holds all their projects
// (/home/user/projects/<project>) and a persistent home (/home/user), so
// toolchains and caches installed once serve every project. Workspace files
// live on host directories the API can read (bind mounts), so read-only
// operations (file tree, content, diffs, search) run on the host via HostPath
// and never wake the box. Mutations and agent processes go through
// Exec/Start, which start it on demand. The Manager stops a box that sat idle
// longer than SANDBOX_IDLE_TTL_SECONDS and holds leases so a busy one is
// never stopped.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	KindDocker = "docker"
	KindHost   = "host"

	// Paths inside a Docker box.
	ContainerHome     = "/home/user"
	ContainerProjects = "/home/user/projects"
	// Claude's config dir inside HOME, the same layout host sandboxes use.
	ContainerClaudeDir = ContainerHome + "/claude"
	ContainerAskpass   = "/usr/local/bin/git-askpass"
	ContainerUser      = "user"
	ContainerUID       = 1000
	TerminalType       = "xterm-256color"
	ContainerPrefix    = "agentrove-sandbox-"
	ManagedLabel       = "agentrove.managed"
	BoxLabel           = "agentrove.box"
	// InstanceLabel scopes containers to the API instance (by storage path),
	// so two instances sharing a Docker daemon never reap each other's boxes.
	InstanceLabel = "agentrove.instance"
)

// BoxID is the sandbox id of a user's Docker box.
func BoxID(userID string) string {
	return "box-" + strings.ReplaceAll(userID, "-", "")
}

var ErrNotFound = errors.New("sandbox not found")

type ExecRequest struct {
	Cmd     []string
	Cwd     string // absolute path in the sandbox; empty = workspace root
	Env     map[string]string
	Stdin   io.Reader
	Timeout time.Duration
	// Root runs as root in Docker sandboxes (ownership fixes).
	Root bool
}

type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

func (r ExecResult) Output() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

// Process is a long-running child with piped stdio (the Claude CLI).
type Process interface {
	Stdin() io.WriteCloser
	Stdout() io.Reader
	Stderr() io.Reader
	Wait() error
	Kill() error
}

// PTY is an interactive terminal process.
type PTY interface {
	io.ReadWriteCloser
	Resize(rows, cols int) error
	Wait() error
}

type CreateOptions struct {
	SandboxID     string
	WorkspacePath string // host path of the workspace directory (host sandboxes)
	UserID        string
}

type Provider interface {
	Kind() string
	Create(ctx context.Context, opts CreateOptions) error
	Delete(ctx context.Context, sandboxID string) error
	EnsureRunning(ctx context.Context, sandboxID string, opts CreateOptions) error
	Stop(ctx context.Context, sandboxID string) error
	// Running lists sandbox ids this provider currently has running.
	Running(ctx context.Context) ([]string, error)
	Exec(ctx context.Context, sandboxID string, req ExecRequest) (ExecResult, error)
	Start(ctx context.Context, sandboxID string, req ExecRequest) (Process, error)
	StartPTY(ctx context.Context, sandboxID string, req ExecRequest, rows, cols int) (PTY, error)
	DiskUsage(ctx context.Context, sandboxID string) (int64, bool)
}

// Sandbox is a workspace-bound handle: it knows the provider, the workspace
// root as seen inside the sandbox, and how to map those paths to the host.
type Sandbox struct {
	ID            string
	Kind          string
	UserID        string
	WorkspacePath string // host path
	Root          string // workspace root inside the sandbox
	Home          string // HOME inside the sandbox
	provider      Provider
	mgr           *Manager
}

func (s *Sandbox) createOpts() CreateOptions {
	return CreateOptions{SandboxID: s.ID, WorkspacePath: s.WorkspacePath, UserID: s.UserID}
}

// Resolve turns a workspace-relative path into an absolute sandbox path.
func (s *Sandbox) Resolve(rel string) string {
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" || rel == "." {
		return s.Root
	}
	return path.Join(s.Root, rel)
}

// HostPath maps an absolute sandbox path (or "" for the root) to the host
// path the API can read. Paths outside the workspace are rejected.
func (s *Sandbox) HostPath(sandboxPath string) (string, error) {
	if sandboxPath == "" {
		return s.WorkspacePath, nil
	}
	clean := path.Clean(sandboxPath)
	if !path.IsAbs(clean) {
		clean = path.Join(s.Root, clean)
	}
	if clean == s.Root {
		return s.WorkspacePath, nil
	}
	rel, ok := strings.CutPrefix(clean, s.Root+"/")
	if !ok {
		return "", fmt.Errorf("path %q is outside the workspace", sandboxPath)
	}
	return filepath.Join(s.WorkspacePath, filepath.FromSlash(rel)), nil
}

// SandboxPath maps a host path back into the sandbox.
func (s *Sandbox) SandboxPath(hostPath string) (string, error) {
	rel, err := filepath.Rel(s.WorkspacePath, hostPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q is outside the workspace", hostPath)
	}
	if rel == "." {
		return s.Root, nil
	}
	return path.Join(s.Root, filepath.ToSlash(rel)), nil
}

func (s *Sandbox) ensure(ctx context.Context) error {
	return s.mgr.ensureRunning(ctx, s)
}

// Exec runs a command to completion inside the sandbox, waking it if needed.
func (s *Sandbox) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if err := s.ensure(ctx); err != nil {
		return ExecResult{}, err
	}
	s.mgr.Touch(s.ID)
	if req.Cwd == "" {
		req.Cwd = s.Root
	}
	req.Env = s.mgr.mergeEnv(s, req.Env)
	if req.Timeout == 0 {
		req.Timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()
	res, err := s.provider.Exec(ctx, s.ID, req)
	s.mgr.Touch(s.ID)
	return res, err
}

// Shell runs a bash command string.
func (s *Sandbox) Shell(ctx context.Context, cwd, script string, timeout time.Duration) (ExecResult, error) {
	return s.Exec(ctx, ExecRequest{Cmd: []string{"bash", "-c", script}, Cwd: cwd, Timeout: timeout})
}

// Start launches a long-running process. The returned release func must be
// called when the process ends; until then the sandbox is never idle-stopped.
func (s *Sandbox) Start(ctx context.Context, req ExecRequest) (Process, func(), error) {
	if err := s.ensure(ctx); err != nil {
		return nil, nil, err
	}
	if req.Cwd == "" {
		req.Cwd = s.Root
	}
	req.Env = s.mgr.mergeEnv(s, req.Env)
	// Turns hold their own lease; the process lease only keeps the box up.
	release := s.mgr.acquire(s.ID, false)
	p, err := s.provider.Start(ctx, s.ID, req)
	if err != nil {
		release()
		return nil, nil, err
	}
	return p, release, nil
}

// StartPTY opens a terminal; release must be called when it closes.
func (s *Sandbox) StartPTY(ctx context.Context, req ExecRequest, rows, cols int) (PTY, func(), error) {
	if err := s.ensure(ctx); err != nil {
		return nil, nil, err
	}
	if req.Cwd == "" {
		req.Cwd = s.Root
	}
	req.Env = s.mgr.mergeEnv(s, req.Env)
	release := s.mgr.Acquire(s.ID)
	p, err := s.provider.StartPTY(ctx, s.ID, req, rows, cols)
	if err != nil {
		release()
		return nil, nil, err
	}
	return p, release, nil
}

// Acquire pins the sandbox awake (e.g. for the length of an agent turn).
func (s *Sandbox) Acquire() func() { return s.mgr.Acquire(s.ID) }

func (s *Sandbox) Touch() { s.mgr.Touch(s.ID) }

func (s *Sandbox) DiskUsage(ctx context.Context) (int64, bool) {
	return s.provider.DiskUsage(ctx, s.ID)
}

// Env returns the full environment agent processes and terminals get.
func (s *Sandbox) Env() map[string]string { return s.mgr.mergeEnv(s, nil) }

// GitAskpassPath is the askpass helper path inside the sandbox.
func (s *Sandbox) GitAskpassPath() string {
	if s.Kind == KindDocker {
		return ContainerAskpass
	}
	return s.mgr.hostAskpassPath()
}
