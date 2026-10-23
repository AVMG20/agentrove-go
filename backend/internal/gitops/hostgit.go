package gitops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

// HostRepo runs read-only git commands against a workspace dir. When the
// user's box is running it runs them *inside* the box (race-free: the repo
// config is the agent's own, and it already has code execution there, so a
// malicious .git/config, .gitattributes, filter/textconv driver or hook can't
// reach the host). Only when the box is stopped — when no agent is running to
// win a TOCTOU — does it fall back to running git on the host, gated by the
// static-config Verify. This keeps diffs/status working while the box sleeps.
type HostRepo struct {
	// Dir is the host directory commands run in (host fallback).
	Dir string
	// WorkTree is set when the git dir had to be translated from a sandbox path.
	WorkTree string
	// root is the host workspace dir; the git dir and work tree must stay in it.
	root string
	opts []string
	env  []string
	// trusted skips Verify (set while Verify itself runs git).
	trusted bool

	// sb and rel drive in-box execution; nil sb means host-only (tests).
	sb  *sandbox.Sandbox
	rel string
	// boxUp reports the box is running; boxExec runs git inside it. Both are
	// set by OpenHost for Docker sandboxes and overridable in tests.
	boxUp   func(ctx context.Context) bool
	boxExec func(ctx context.Context, dir string, args []string) (Result, error)
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
		rel:  rel,
		root: sb.WorkspacePath,
		opts: []string{"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "-c", "color.ui=false"},
		// GIT_OPTIONAL_LOCKS=0 keeps `git status` from rewriting the index: the
		// API may run as a different uid than the sandbox user.
		env: append(os.Environ(), "GIT_DISCOVERY_ACROSS_FILESYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0"),
	}
	if sb.Kind == sandbox.KindDocker {
		r.sb = sb
		// Decide host-vs-box once per repo (one request): a single diff fans out
		// into many git commands, and each state probe is a docker inspect.
		var upOnce sync.Once
		var up bool
		r.boxUp = func(ctx context.Context) bool {
			upOnce.Do(func() { up = sb.IsRunning(ctx) })
			return up
		}
		r.boxExec = r.execInBox
		// The container can't see above its workspace mount, so neither should we.
		r.env = append(r.env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(filepath.Clean(sb.WorkspacePath)))
		if gitDir, workTree, ok := translatedGitDir(sb, dir); ok {
			r.WorkTree = workTree
			r.opts = append(r.opts, "--git-dir="+gitDir, "--work-tree="+workTree)
		}
	}
	return r, nil
}

// execInBox runs git inside the user's box as the agent user. The agent owns
// this box, so agent-controlled config is harmless here; no Verify is needed.
// dir is a host path, mapped back to its container path; worktrees resolve
// their .git natively, so no --git-dir/--work-tree translation is passed.
func (r *HostRepo) execInBox(ctx context.Context, dir string, args []string) (Result, error) {
	cwd := r.sb.Resolve(r.rel)
	if dir != "" && dir != r.Dir {
		if c, err := r.sb.SandboxPath(dir); err == nil {
			cwd = c
		}
	}
	opts := []string{"-c", "safe.directory=*", "-c", "core.fsmonitor=false", "-c", "color.ui=false"}
	cmd := append(append([]string{"git"}, opts...), args...)
	res, err := r.sb.Exec(ctx, sandbox.ExecRequest{
		Cmd: cmd, Cwd: cwd, Timeout: hostGitTimeout,
		Env: map[string]string{"GIT_OPTIONAL_LOCKS": "0", "GIT_TERMINAL_PROMPT": "0", "GIT_DISCOVERY_ACROSS_FILESYSTEM": "1"},
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Stdout: res.Stdout, Stderr: res.Stderr, Exit: res.ExitCode}, nil
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

// maxHostGitOutput bounds one command's stdout: the agent controls the repo,
// so a multi-GB untracked file must not turn a diff into an API OOM.
const maxHostGitOutput = 64 << 20

// capBuffer collects up to max bytes. Past that it either fails the write
// (which stops the command) or, with drop, silently discards the rest.
type capBuffer struct {
	buf      bytes.Buffer
	max      int
	drop     bool
	overflow bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.max {
		if c.drop {
			return len(p), nil
		}
		c.overflow = true
		return 0, errors.New("git output too large")
	}
	return c.buf.Write(p)
}

// unsafeConfigRE matches repo-local settings that make git run a program (or
// read another tree) when it diffs or stats the work tree. The repo config is
// written by the agent, and these commands run on the host, outside the box.
var unsafeConfigRE = regexp.MustCompile(`^(include\.|includeif\.|core\.worktree$|diff\.external$|diff\..+\.(textconv|command)$|filter\..+\.(clean|smudge|process)$)`)

// unsafeConfigKey returns the first agent-controlled (local or worktree scope)
// setting in `git config --list --show-scope -z` output that isn't safe on
// the host. System and global config belong to the operator and are ignored.
func unsafeConfigKey(out string) string {
	// -z with --show-scope: "scope\0key\nvalue\0" per entry.
	recs := strings.Split(out, "\x00")
	for i := 0; i+1 < len(recs); i += 2 {
		if scope := recs[i]; scope != "local" && scope != "worktree" {
			continue
		}
		key, _, _ := strings.Cut(recs[i+1], "\n")
		if unsafeConfigRE.MatchString(strings.ToLower(key)) {
			return key
		}
	}
	return ""
}

// Verify refuses a repository that could make host-side git execute agent
// code (filters, external diff drivers) or read outside the workspace (an
// absolute core.worktree, a `.git` file or commondir pointing elsewhere). A
// non-repo passes: the commands that follow simply fail.
func (r *HostRepo) Verify(ctx context.Context) error {
	if r.trusted {
		return nil
	}
	// When the box is running, git runs inside it (RunIn dispatches there), so
	// the host never touches the agent's config and there is nothing to verify.
	if r.boxExec != nil && r.boxUp(ctx) {
		return nil
	}
	t := *r
	t.trusted = true
	res, err := t.Run(ctx, "rev-parse", "--path-format=absolute", "--absolute-git-dir", "--git-common-dir", "--show-toplevel")
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) < 2 || lines[0] == "" {
		return nil
	}
	for _, p := range lines {
		if !withinReal(r.root, p) {
			return httpx.BadRequest("Repository points outside the workspace; not inspecting it on the host")
		}
	}
	cfg, err := t.Run(ctx, "config", "--list", "--show-scope", "-z")
	if err != nil {
		return err
	}
	if cfg.Exit != 0 {
		return httpx.BadRequest("Could not read the repository config")
	}
	if key := unsafeConfigKey(cfg.Stdout); key != "" {
		return httpx.BadRequest("Repository config sets " + key + ", which is not allowed for host-side git; remove it in the terminal")
	}
	return nil
}

// RunIn executes git in dir with extra env entries. It runs inside the box
// when it is up (race-free) and on the host otherwise (Verify-gated).
func (r *HostRepo) RunIn(ctx context.Context, dir string, env []string, args ...string) (Result, error) {
	if r.boxExec != nil && r.boxUp(ctx) {
		return r.boxExec(ctx, dir, args)
	}
	if err := r.Verify(ctx); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, hostGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(append([]string{}, r.opts...), args...)...)
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, r.env...), env...)
	stdout, stderr := &capBuffer{max: maxHostGitOutput}, &capBuffer{max: 1 << 20, drop: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if stdout.overflow {
		return Result{}, httpx.BadRequest("git output is too large to show")
	}
	res := Result{Stdout: stdout.buf.String(), Stderr: stderr.buf.String()}
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

// TopLevel is the work tree root as a host path. When git ran inside the box,
// rev-parse returns a container path, which is mapped back to the host so
// callers that feed it to RunIn (which maps host->container) round-trip.
func (r *HostRepo) TopLevel(ctx context.Context) string {
	if r.WorkTree != "" {
		return r.WorkTree
	}
	res, err := r.Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil || res.Exit != 0 {
		return r.Dir
	}
	top := strings.TrimSpace(res.Stdout)
	if top == "" {
		return r.Dir
	}
	if r.sb != nil {
		if host, err := r.sb.HostPath(top); err == nil {
			return host
		}
	}
	return top
}
