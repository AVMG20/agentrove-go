package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

// HostRepo runs read-only git commands on the host against a workspace dir,
// so diffs, status and branch lists work while a Docker sandbox is stopped.
type HostRepo struct {
	// Dir is the host directory commands run in.
	Dir string
	// WorkTree is set when the git dir had to be translated from a sandbox path.
	WorkTree string
	opts     []string
	env      []string
}

type Result struct {
	Stdout string
	Stderr string
	Exit   int
}

const hostGitTimeout = time.Minute

// HostDir maps a workspace-relative dir to the host, rejecting anything that
// escapes the workspace (including through symlinks) or isn't a directory.
func HostDir(sb *sandbox.Sandbox, rel string) (string, error) {
	host, err := sb.HostPath(sb.Resolve(rel))
	if err != nil {
		return "", httpx.BadRequest(err.Error())
	}
	if !withinReal(sb.WorkspacePath, host) {
		return "", httpx.BadRequest("Path escapes workspace root: " + rel)
	}
	fi, err := os.Stat(host)
	if err != nil || !fi.IsDir() {
		return "", httpx.BadRequest("Directory not found: " + displayRel(rel))
	}
	return host, nil
}

func displayRel(rel string) string {
	if rel == "" {
		return "."
	}
	return rel
}

// withinReal reports whether target (after resolving symlinks) is root or
// below it. A missing target is checked lexically.
func withinReal(root, target string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = filepath.Clean(root)
	}
	realTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		realTarget = filepath.Clean(target)
		if r2, err2 := filepath.EvalSymlinks(filepath.Dir(target)); err2 == nil {
			realTarget = filepath.Join(r2, filepath.Base(target))
		}
	}
	rel, err := filepath.Rel(realRoot, realTarget)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// OpenHost prepares git for the workspace dir rel. Worktrees created inside a
// Docker sandbox have a `.git` file pointing at a container path
// (/home/user/workspace/.git/worktrees/<id>); that is translated to the host
// path and passed explicitly via --git-dir/--work-tree.
func OpenHost(sb *sandbox.Sandbox, rel string) (*HostRepo, error) {
	dir, err := HostDir(sb, rel)
	if err != nil {
		return nil, err
	}
	r := &HostRepo{
		Dir:  dir,
		opts: []string{"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "-c", "color.ui=false"},
		// GIT_OPTIONAL_LOCKS=0 keeps `git status` from rewriting the index: the
		// API may run as a different uid than the sandbox user.
		env: append(os.Environ(), "GIT_DISCOVERY_ACROSS_FILESYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"),
	}
	if sb.Kind == sandbox.KindDocker {
		// The container can't see above its workspace mount, so neither should we.
		r.env = append(r.env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(filepath.Clean(sb.WorkspacePath)))
		if gitDir, workTree, ok := translatedGitDir(sb, dir); ok {
			r.WorkTree = workTree
			r.opts = append(r.opts, "--git-dir="+gitDir, "--work-tree="+workTree)
		}
	}
	return r, nil
}

// translatedGitDir walks up from dir to the workspace root looking for the
// nearest `.git`; when it's a file whose gitdir is an absolute sandbox path,
// it returns the host gitdir and the work tree (the dir holding the file).
func translatedGitDir(sb *sandbox.Sandbox, dir string) (gitDir, workTree string, ok bool) {
	root := filepath.Clean(sb.WorkspacePath)
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		fi, err := os.Lstat(filepath.Join(d, ".git"))
		if err == nil {
			if !fi.Mode().IsRegular() {
				return "", "", false
			}
			content, err := os.ReadFile(filepath.Join(d, ".git"))
			if err != nil {
				return "", "", false
			}
			target, found := parseGitFile(string(content))
			if !found || !strings.HasPrefix(target, "/") {
				return "", "", false
			}
			host, err := sb.HostPath(target)
			if err != nil || host == target {
				return "", "", false
			}
			return host, d, true
		}
		if d == root || len(d) <= len(root) {
			return "", "", false
		}
	}
}

// parseGitFile reads the `gitdir: <path>` line of a .git file.
func parseGitFile(content string) (string, bool) {
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:"); ok {
			v = strings.TrimSpace(v)
			return v, v != ""
		}
	}
	return "", false
}

// Run executes git with args in r.Dir.
func (r *HostRepo) Run(ctx context.Context, args ...string) (Result, error) {
	return r.RunIn(ctx, r.Dir, nil, args...)
}

// RunIn executes git in dir with extra env entries.
func (r *HostRepo) RunIn(ctx context.Context, dir string, env []string, args ...string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, hostGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, r.opts...), args...)...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, r.env...), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		res.Exit = exitErr.ExitCode()
		if ctx.Err() != nil {
			return res, httpx.BadRequest("git command timed out")
		}
		return res, nil
	}
	if err != nil {
		return res, err
	}
	return res, nil
}

// IsRepo reports whether r.Dir is inside a git work tree.
func (r *HostRepo) IsRepo(ctx context.Context) bool {
	res, err := r.Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && res.Exit == 0 && strings.TrimSpace(res.Stdout) == "true"
}

// TopLevel is the work tree root on the host.
func (r *HostRepo) TopLevel(ctx context.Context) string {
	if r.WorkTree != "" {
		return r.WorkTree
	}
	res, err := r.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil || res.Exit != 0 {
		return r.Dir
	}
	if top := strings.TrimSpace(res.Stdout); top != "" {
		return top
	}
	return r.Dir
}
