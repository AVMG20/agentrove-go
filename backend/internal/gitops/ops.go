package gitops

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"agentrove/internal/httpx"
	"agentrove/internal/sandbox"
)

// Response shapes (app/models/schemas/sandbox.py).

type DiffResponse struct {
	Diff       string  `json:"diff"`
	HasChanges bool    `json:"has_changes"`
	IsGitRepo  bool    `json:"is_git_repo"`
	Error      *string `json:"error"`
}

type DiffTotals struct {
	Additions    int `json:"additions"`
	Deletions    int `json:"deletions"`
	FilesChanged int `json:"files_changed"`
}

type DiffStatsResponse struct {
	Uncommitted DiffTotals `json:"uncommitted"`
	Branch      DiffTotals `json:"branch"`
	IsGitRepo   bool       `json:"is_git_repo"`
	HeadSHA     *string    `json:"head_sha"`
}

type FetchBaseResponse struct {
	Fetched bool `json:"fetched"`
}

type FileBaselineResponse struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	IsGitRepo bool   `json:"is_git_repo"`
}

type ChangedPathsResponse struct {
	Paths     []string `json:"paths"`
	IsGitRepo bool     `json:"is_git_repo"`
}

type BranchesResponse struct {
	Branches      []string `json:"branches"`
	CurrentBranch string   `json:"current_branch"`
	IsGitRepo     bool     `json:"is_git_repo"`
}

type CheckoutResponse struct {
	Success       bool    `json:"success"`
	CurrentBranch string  `json:"current_branch"`
	Error         *string `json:"error"`
}

type CommandResponse struct {
	Success bool    `json:"success"`
	Output  string  `json:"output"`
	Error   *string `json:"error"`
}

type RemoteURLResponse struct {
	Owner     string `json:"owner"`
	Repo      string `json:"repo"`
	RemoteURL string `json:"remote_url"`
}

func strPtr(s string) *string { return &s }

// ---- read-only operations (host) ----

// DetectBase finds the default branch to diff against: the remote HEAD
// symref, then origin/{main,master,develop,trunk}, then the local ones.
func DetectBase(ctx context.Context, r *HostRepo) string {
	if res, err := r.Run(ctx, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil && res.Exit == 0 {
		if ref := strings.TrimPrefix(strings.TrimSpace(res.Stdout), "refs/remotes/"); ref != "" {
			return ref
		}
	}
	candidates := []string{"main", "master", "develop", "trunk"}
	for _, b := range candidates {
		if res, err := r.Run(ctx, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+b); err == nil && res.Exit == 0 {
			return "origin/" + b
		}
	}
	for _, b := range candidates {
		if res, err := r.Run(ctx, "rev-parse", "--verify", b); err == nil && res.Exit == 0 {
			return b
		}
	}
	return ""
}

func mergeBase(ctx context.Context, r *HostRepo, base string) string {
	res, err := r.Run(ctx, "merge-base", base, "HEAD")
	if err == nil && res.Exit == 0 {
		if mb := strings.TrimSpace(res.Stdout); mb != "" {
			return mb
		}
	}
	return base
}

func emptyTree(ctx context.Context, r *HostRepo) string {
	res, err := r.Run(ctx, "hash-object", "-t", "tree", "/dev/null")
	if err == nil && res.Exit == 0 {
		if t := strings.TrimSpace(res.Stdout); t != "" {
			return t
		}
	}
	return "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
}

// Diff returns the patch for "all" (working tree + untracked vs HEAD) or
// "branch" (HEAD vs merge-base with the default branch).
func Diff(ctx context.Context, r *HostRepo, mode string, fullContext bool) (DiffResponse, error) {
	if !r.IsRepo(ctx) {
		return DiffResponse{}, nil
	}
	var ctxArgs []string
	if fullContext {
		ctxArgs = []string{"-U99999"}
	}
	if mode == "branch" {
		base := DetectBase(ctx, r)
		if base == "" {
			return DiffResponse{IsGitRepo: true, Error: strPtr("Could not determine base branch")}, nil
		}
		res, err := r.Run(ctx, append(append([]string{"diff"}, ctxArgs...), mergeBase(ctx, r, base), "HEAD")...)
		if err != nil {
			return DiffResponse{}, err
		}
		out := ""
		if res.Exit == 0 {
			out = res.Stdout
		}
		return DiffResponse{Diff: out, HasChanges: strings.TrimSpace(out) != "", IsGitRepo: true}, nil
	}

	var buf strings.Builder
	res, err := r.Run(ctx, append(append([]string{"diff"}, ctxArgs...), "HEAD")...)
	if err != nil {
		return DiffResponse{}, err
	}
	if res.Exit != 0 {
		// Unborn branch: diff against the empty tree for the same combined view.
		res, err = r.Run(ctx, append(append([]string{"diff"}, ctxArgs...), emptyTree(ctx, r))...)
		if err != nil {
			return DiffResponse{}, err
		}
	}
	buf.WriteString(res.Stdout)
	for _, f := range untrackedFiles(ctx, r, r.Dir) {
		res, err := r.Run(ctx, append(append([]string{"diff"}, ctxArgs...), "--no-index", "--", "/dev/null", f)...)
		if err != nil {
			return DiffResponse{}, err
		}
		buf.WriteString(res.Stdout)
	}
	out := buf.String()
	return DiffResponse{Diff: out, HasChanges: strings.TrimSpace(out) != "", IsGitRepo: true}, nil
}

// untrackedFiles lists untracked, non-ignored files relative to dir.
func untrackedFiles(ctx context.Context, r *HostRepo, dir string) []string {
	res, err := r.RunIn(ctx, dir, nil, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || res.Exit != 0 {
		return nil
	}
	var out []string
	for _, f := range strings.Split(res.Stdout, "\x00") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// DiffStats sums numstat for uncommitted changes (tracked + untracked) and for
// the branch vs its merge-base. Untracked files are counted directly instead
// of via `git add -N` on a temp index so the host never writes git objects.
func DiffStats(ctx context.Context, r *HostRepo) (DiffStatsResponse, error) {
	if !r.IsRepo(ctx) {
		return DiffStatsResponse{}, nil
	}
	top := r.TopLevel(ctx)
	run := func(args ...string) string {
		res, err := r.RunIn(ctx, top, nil, args...)
		if err != nil || res.Exit != 0 {
			return ""
		}
		return res.Stdout
	}
	out := DiffStatsResponse{IsGitRepo: true}
	head := strings.TrimSpace(run("rev-parse", "--verify", "-q", "HEAD"))
	if head != "" {
		out.HeadSHA = &head
	}
	against := head
	if against == "" {
		against = emptyTree(ctx, r)
	}
	out.Uncommitted = SumNumstat(run("diff", "--numstat", against))
	for _, f := range untrackedFiles(ctx, r, top) {
		if strings.HasSuffix(f, "/") {
			continue
		}
		lines, ok := countLines(filepath.Join(top, filepath.FromSlash(f)))
		if !ok {
			continue
		}
		out.Uncommitted.FilesChanged++
		out.Uncommitted.Additions += lines
	}
	if base := DetectBase(ctx, r); base != "" {
		out.Branch = SumNumstat(run("diff", "--numstat", mergeBase(ctx, r, base), "HEAD"))
	}
	return out, nil
}

// countLines counts lines the way numstat does for a new file: binary files
// (NUL in the first 8000 bytes) count 0, a missing final newline still counts.
func countLines(p string) (int, bool) {
	fi, err := os.Lstat(p)
	if err != nil {
		return 0, false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return 1, true
	}
	if !fi.Mode().IsRegular() {
		return 0, false
	}
	f, err := os.Open(p)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	return CountTextLines(f), true
}

// CountTextLines returns the numstat addition count of a file's content.
func CountTextLines(rd io.Reader) int {
	buf := make([]byte, 64*1024)
	lines, total := 0, 0
	var last byte
	for {
		n, err := rd.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if total < 8000 && bytes.IndexByte(chunk[:min(len(chunk), 8000-total)], 0) >= 0 {
				return 0
			}
			total += n
			lines += bytes.Count(chunk, []byte{'\n'})
			last = chunk[n-1]
		}
		if err != nil {
			break
		}
	}
	if total > 0 && last != '\n' {
		lines++
	}
	return lines
}

// SumNumstat totals `git diff --numstat` output; binary files ("-") count as
// changed files without line counts.
func SumNumstat(output string) DiffTotals {
	var t DiffTotals
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		t.FilesChanged++
		if n, ok := atoiDigits(parts[0]); ok {
			t.Additions += n
		}
		if n, ok := atoiDigits(parts[1]); ok {
			t.Deletions += n
		}
	}
	return t
}

func atoiDigits(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// FileBaseline returns the HEAD version of path (relative to the cwd).
func FileBaseline(ctx context.Context, r *HostRepo, p string) (FileBaselineResponse, error) {
	if err := ValidateRelativePath(p); err != nil {
		return FileBaselineResponse{}, err
	}
	if !r.IsRepo(ctx) {
		return FileBaselineResponse{Path: p, IsGitRepo: false}, nil
	}
	res, err := r.Run(ctx, "show", "HEAD:./"+p)
	if err != nil {
		return FileBaselineResponse{}, err
	}
	content := ""
	if res.Exit == 0 {
		content = strings.ToValidUTF8(res.Stdout, "�")
	}
	return FileBaselineResponse{Path: p, Content: content, IsGitRepo: true}, nil
}

// ChangedPaths lists paths with uncommitted changes, relative to the cwd.
func ChangedPaths(ctx context.Context, r *HostRepo) (ChangedPathsResponse, error) {
	if !r.IsRepo(ctx) {
		return ChangedPathsResponse{Paths: []string{}}, nil
	}
	prefix, err := r.Run(ctx, "rev-parse", "--show-prefix")
	if err != nil {
		return ChangedPathsResponse{}, err
	}
	status, err := r.Run(ctx, "status", "--porcelain", "--no-renames", "--untracked-files=all", "-z")
	if err != nil {
		return ChangedPathsResponse{}, err
	}
	return ChangedPathsResponse{Paths: ParseStatusPaths(strings.TrimSpace(prefix.Stdout), status.Stdout), IsGitRepo: true}, nil
}

// ParseStatusPaths turns `status --porcelain -z` records (repo-root paths)
// into paths relative to prefix, dropping those outside it.
func ParseStatusPaths(prefix, statusOut string) []string {
	paths := []string{}
	for _, entry := range strings.Split(statusOut, "\x00") {
		if len(entry) <= 3 {
			continue
		}
		p := entry[3:]
		if prefix != "" {
			rest, ok := strings.CutPrefix(p, prefix)
			if !ok {
				continue
			}
			p = rest
		}
		paths = append(paths, p)
	}
	return paths
}

// Branches lists local branches plus origin-only ones.
func Branches(ctx context.Context, r *HostRepo) (BranchesResponse, error) {
	empty := BranchesResponse{Branches: []string{}}
	if !r.IsRepo(ctx) {
		return empty, nil
	}
	cur, err := r.Run(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return empty, err
	}
	local, err := r.Run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return empty, err
	}
	remote, err := r.Run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin")
	if err != nil {
		return empty, err
	}
	current := ""
	if cur.Exit == 0 {
		current = strings.TrimSpace(cur.Stdout)
	}
	return BranchesResponse{Branches: MergeBranches(local.Stdout, remote.Stdout), CurrentBranch: current, IsGitRepo: true}, nil
}

// MergeBranches unions local branch names with origin/* names (prefix
// stripped, origin/HEAD skipped), sorted.
func MergeBranches(localOut, remoteOut string) []string {
	set := map[string]bool{}
	for _, line := range strings.Split(localOut, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			set[name] = true
		}
	}
	for _, line := range strings.Split(remoteOut, "\n") {
		name := strings.TrimSpace(line)
		if name == "" || name == "origin/HEAD" || !strings.HasPrefix(name, "origin/") {
			continue
		}
		set[strings.TrimPrefix(name, "origin/")] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

var githubRemoteRE = regexp.MustCompile(`^(?:https?://github\.com/|git@github\.com:)([^/]+)/([^/]+?)(?:\.git)?$`)

// ParseGitHubRemote extracts owner/repo from a github.com remote URL.
func ParseGitHubRemote(url string) (owner, repo string, ok bool) {
	m := githubRemoteRE.FindStringSubmatch(url)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func RemoteURL(ctx context.Context, r *HostRepo) (RemoteURLResponse, error) {
	res, err := r.Run(ctx, "remote", "get-url", "origin")
	if err != nil {
		return RemoteURLResponse{}, err
	}
	if res.Exit != 0 {
		return RemoteURLResponse{}, httpx.NotFound("No git remote origin found")
	}
	url := strings.TrimSpace(res.Stdout)
	owner, repo, ok := ParseGitHubRemote(url)
	if !ok {
		return RemoteURLResponse{}, httpx.BadRequest("No GitHub remote detected — only github.com remotes are supported")
	}
	return RemoteURLResponse{Owner: owner, Repo: repo, RemoteURL: url}, nil
}

// ---- mutations (inside the sandbox, as the agent's user with its creds) ----

const (
	discoveryEnv = "export GIT_DISCOVERY_ACROSS_FILESYSTEM=1 && "
	cdRepoRoot   = `cd "$(git rev-parse --show-toplevel)" && `

	detectBaseScript = `base=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's|refs/remotes/||');` +
		` [ -z "$base" ] && for b in main master develop trunk; do` +
		` git rev-parse --verify -q "refs/remotes/origin/$b" >/dev/null && base=origin/$b && break; done;` +
		` [ -z "$base" ] && for b in main master develop trunk; do` +
		` git rev-parse --verify $b >/dev/null 2>&1 && base=$b && break; done;`

	fetchBaseScript = "git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 2; " +
		`cd "$(git rev-parse --show-toplevel)"; ` +
		detectBaseScript +
		` fp=$(git rev-parse --git-path FETCH_HEAD);` +
		` if [ -z "$(find "$fp" -mmin -1 2>/dev/null)" ]; then` +
		` case "$base" in origin/*)` +
		` t=; command -v timeout >/dev/null 2>&1 && t='timeout 15';` +
		` GIT_TERMINAL_PROMPT=0` +
		` GIT_SSH_COMMAND="${GIT_SSH_COMMAND:-ssh -o BatchMode=yes -o ConnectTimeout=10}"` +
		` $t git -c http.lowSpeedLimit=1000 -c http.lowSpeedTime=10` +
		` fetch --quiet --no-tags origin "${base#origin/}" >/dev/null 2>&1 && printf fetched;;` +
		` esac;` +
		` fi; true`

	restoreAllScript = cdRepoRoot + "if git rev-parse --verify HEAD >/dev/null 2>&1; then " +
		"git reset --hard HEAD && git clean -fd; " +
		"else " +
		"git rm --cached -rf --ignore-unmatch -q . >/dev/null 2>&1; " +
		"git clean -fd; " +
		"fi"
)

func shell(ctx context.Context, sb *sandbox.Sandbox, rel, script string, timeout time.Duration) (sandbox.ExecResult, error) {
	res, err := sb.Shell(ctx, sb.Resolve(rel), discoveryEnv+script, timeout)
	if err != nil {
		return res, httpx.BadRequest(err.Error())
	}
	return res, nil
}

// RunCommand runs a git script in the sandbox and reports it the way the
// Python GitCommandResponse did.
func RunCommand(ctx context.Context, sb *sandbox.Sandbox, rel, script string, timeout time.Duration) (CommandResponse, error) {
	res, err := shell(ctx, sb, rel, "{ "+script+"; } 2>&1", timeout)
	if err != nil {
		return CommandResponse{}, err
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stdout)
		if msg == "" {
			msg = strings.TrimSpace(res.Stderr)
		}
		return CommandResponse{Success: false, Output: "", Error: &msg}, nil
	}
	return CommandResponse{Success: true, Output: strings.TrimSpace(res.Stdout)}, nil
}

func Push(ctx context.Context, sb *sandbox.Sandbox, rel string) (CommandResponse, error) {
	return RunCommand(ctx, sb, rel, "git push -u origin HEAD", 5*time.Minute)
}

func Pull(ctx context.Context, sb *sandbox.Sandbox, rel string) (CommandResponse, error) {
	return RunCommand(ctx, sb, rel, "git pull", 5*time.Minute)
}

func Commit(ctx context.Context, sb *sandbox.Sandbox, rel, message string) (CommandResponse, error) {
	return RunCommand(ctx, sb, rel, "git add -A && git commit -m "+shq(message), 2*time.Minute)
}

func RestoreAll(ctx context.Context, sb *sandbox.Sandbox, rel string) (CommandResponse, error) {
	return RunCommand(ctx, sb, rel, restoreAllScript, 2*time.Minute)
}

// RestoreScript builds the restore-file script. Paths are repo-root relative.
func RestoreScript(filePath, oldPath string) string {
	fp := shq(filePath)
	if oldPath != "" && oldPath != filePath {
		// Drop the new path only once the old one is back, so a failed checkout
		// can't turn a committed rename into a deletion.
		return cdRepoRoot + "git checkout HEAD -- " + shq(oldPath) +
			" && { git reset -- " + fp + " >/dev/null 2>&1; rm -f -- " + fp + "; }"
	}
	return cdRepoRoot + "if git cat-file -e HEAD:" + fp + " 2>/dev/null; then " +
		"git checkout HEAD -- " + fp + "; " +
		"else " +
		"git reset -- " + fp + " >/dev/null 2>&1; " +
		"rm -f -- " + fp + "; " +
		"fi"
}

func RestoreFile(ctx context.Context, sb *sandbox.Sandbox, rel, filePath, oldPath string) (CommandResponse, error) {
	if err := ValidateRelativePath(filePath); err != nil {
		return CommandResponse{}, err
	}
	if oldPath != "" {
		if err := ValidateRelativePath(oldPath); err != nil {
			return CommandResponse{}, err
		}
	}
	return RunCommand(ctx, sb, rel, RestoreScript(filePath, oldPath), 2*time.Minute)
}

func FetchBase(ctx context.Context, sb *sandbox.Sandbox, rel string) (FetchBaseResponse, error) {
	res, err := shell(ctx, sb, rel, fetchBaseScript, time.Minute)
	if err != nil {
		return FetchBaseResponse{}, err
	}
	if res.ExitCode == 2 {
		return FetchBaseResponse{}, nil
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(res.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(res.Stdout)
		}
		if detail == "" {
			return FetchBaseResponse{}, httpx.BadRequest("Failed to fetch base branch")
		}
		return FetchBaseResponse{}, httpx.BadRequest("Failed to fetch base branch: " + detail)
	}
	return FetchBaseResponse{Fetched: strings.TrimSpace(res.Stdout) == "fetched"}, nil
}

func currentBranch(ctx context.Context, sb *sandbox.Sandbox, rel string) string {
	res, err := shell(ctx, sb, rel, "git rev-parse --abbrev-ref HEAD 2>/dev/null", 30*time.Second)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}

func Checkout(ctx context.Context, sb *sandbox.Sandbox, rel, branch string) (CheckoutResponse, error) {
	if err := ValidateBranchName(branch, "branch"); err != nil {
		return CheckoutResponse{}, err
	}
	res, err := shell(ctx, sb, rel, "git checkout "+shq(branch)+" 2>&1", 2*time.Minute)
	if err != nil {
		return CheckoutResponse{}, err
	}
	if res.ExitCode != 0 {
		local, err := shell(ctx, sb, rel, "git show-ref --verify --quiet refs/heads/"+shq(branch), 30*time.Second)
		if err != nil {
			return CheckoutResponse{}, err
		}
		if local.ExitCode == 1 {
			res, err = shell(ctx, sb, rel, "git checkout -b "+shq(branch)+" "+shq("origin/"+branch)+" 2>&1", 2*time.Minute)
			if err != nil {
				return CheckoutResponse{}, err
			}
		}
	}
	current := currentBranch(ctx, sb, rel)
	if res.ExitCode != 0 && current != branch {
		msg := strings.TrimSpace(res.Stdout)
		if msg == "" {
			msg = strings.TrimSpace(res.Stderr)
		}
		return CheckoutResponse{Success: false, Error: &msg}, nil
	}
	if current == "HEAD" {
		// Detached HEAD (tag/SHA): go back so the UI always has a named branch.
		_, _ = shell(ctx, sb, rel, "git checkout - 2>/dev/null", time.Minute)
		return CheckoutResponse{Success: false, Error: strPtr("Cannot checkout: would result in detached HEAD")}, nil
	}
	return CheckoutResponse{Success: true, CurrentBranch: current}, nil
}

func CreateBranch(ctx context.Context, sb *sandbox.Sandbox, rel, name, baseBranch string) (CheckoutResponse, error) {
	if err := ValidateBranchName(name, "branch"); err != nil {
		return CheckoutResponse{}, err
	}
	if baseBranch != "" {
		if err := ValidateBranchName(baseBranch, "base branch"); err != nil {
			return CheckoutResponse{}, err
		}
	}
	base := ""
	if baseBranch != "" {
		base = " " + shq(baseBranch)
	}
	res, err := shell(ctx, sb, rel, "git checkout -b "+shq(name)+base+" 2>&1", 2*time.Minute)
	if err != nil {
		return CheckoutResponse{}, err
	}
	if res.ExitCode != 0 && baseBranch != "" {
		// The base may only exist as a remote-tracking branch.
		res, err = shell(ctx, sb, rel, "git checkout -b "+shq(name)+" "+shq("origin/"+baseBranch)+" 2>&1", 2*time.Minute)
		if err != nil {
			return CheckoutResponse{}, err
		}
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stdout)
		if msg == "" {
			msg = strings.TrimSpace(res.Stderr)
		}
		return CheckoutResponse{Success: false, Error: &msg}, nil
	}
	return CheckoutResponse{Success: true, CurrentBranch: currentBranch(ctx, sb, rel)}, nil
}
