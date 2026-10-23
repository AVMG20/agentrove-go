package gitops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentrove/internal/sandbox"
)

func TestNormalizeRel(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "", false},
		{".", "", false},
		{"./", "", false},
		{"./src", "src", false},
		{".worktrees/abcd1234", ".worktrees/abcd1234", false},
		{"/etc", "", true},
		{"../x", "", true},
		{"a/../../b", "", true},
		{"a'b", "", true},
		{"a\nb", "", true},
		{"a..b", "a..b", false},
	}
	for _, c := range cases {
		got, err := NormalizeRel(c.in)
		if (err != nil) != c.wantErr || got != c.want {
			t.Errorf("NormalizeRel(%q) = %q, %v; want %q, err=%v", c.in, got, err, c.want, c.wantErr)
		}
	}
}

func TestResolveCwdAbsolute(t *testing.T) {
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/workspace", WorkspacePath: "/data/ws"}
	cases := map[string]string{
		"/home/user/workspace":                     "",
		"/home/user/workspace/.worktrees/abcd1234": ".worktrees/abcd1234",
		".worktrees/abcd1234":                      ".worktrees/abcd1234",
	}
	for in, want := range cases {
		got, err := ResolveCwd(sb, in)
		if err != nil || got != want {
			t.Errorf("ResolveCwd(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"/home/user", "/etc/passwd", "../x"} {
		if _, err := ResolveCwd(sb, bad); err == nil {
			t.Errorf("ResolveCwd(%q) should fail", bad)
		}
	}
}

func TestValidators(t *testing.T) {
	for name, ok := range map[string]bool{
		"main": true, "feature/x-1": true, "v1.2": true, "-x": false, "a..b": false, "...": false,
		"a b": false, "a;b": false, "a'b": false, "": false,
	} {
		if got := ValidateBranchName(name, "branch") == nil; got != ok {
			t.Errorf("ValidateBranchName(%q) ok=%v want %v", name, got, ok)
		}
	}
	for p, ok := range map[string]bool{
		"src/a.go": true, "a b.txt": true, "/abs": false, "-rf": false, "../x": false, "a/../b": false,
		"a\nb": false, "": false,
	} {
		if got := ValidateRelativePath(p) == nil; got != ok {
			t.Errorf("ValidateRelativePath(%q) ok=%v want %v", p, got, ok)
		}
	}
}

func TestSumNumstat(t *testing.T) {
	cases := []struct {
		in   string
		want DiffTotals
	}{
		{"", DiffTotals{}},
		{"3\t1\ta.go\n10\t0\tb.go\n", DiffTotals{Additions: 13, Deletions: 1, FilesChanged: 2}},
		{"-\t-\timg.png\n2\t2\tx\n", DiffTotals{Additions: 2, Deletions: 2, FilesChanged: 2}},
		{"garbage\n1\t2\n", DiffTotals{}},
		{"1\t1\tpath\twith\ttabs\n", DiffTotals{Additions: 1, Deletions: 1, FilesChanged: 1}},
	}
	for _, c := range cases {
		if got := SumNumstat(c.in); got != c.want {
			t.Errorf("SumNumstat(%q) = %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestParseStatusPaths(t *testing.T) {
	out := " M src/a.go\x00?? src/new file.txt\x00D  other/b.go\x00"
	if got := ParseStatusPaths("", out); !slices.Equal(got, []string{"src/a.go", "src/new file.txt", "other/b.go"}) {
		t.Errorf("root: %v", got)
	}
	if got := ParseStatusPaths("src/", out); !slices.Equal(got, []string{"a.go", "new file.txt"}) {
		t.Errorf("prefixed: %v", got)
	}
	if got := ParseStatusPaths("", ""); got == nil || len(got) != 0 {
		t.Errorf("empty: %#v", got)
	}
}

func TestMergeBranches(t *testing.T) {
	got := MergeBranches("main\nfeature\n", "origin/HEAD\norigin\norigin/main\norigin/remote-only\n")
	want := []string{"feature", "main", "remote-only"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseGitHubRemote(t *testing.T) {
	cases := []struct{ in, owner, repo string }{
		{"https://github.com/acme/widget.git", "acme", "widget"},
		{"https://github.com/acme/widget", "acme", "widget"},
		{"git@github.com:acme/my.repo.git", "acme", "my.repo"},
		{"http://github.com/a/b", "a", "b"},
	}
	for _, c := range cases {
		o, r, ok := ParseGitHubRemote(c.in)
		if !ok || o != c.owner || r != c.repo {
			t.Errorf("%s -> %s %s %v", c.in, o, r, ok)
		}
	}
	for _, bad := range []string{"https://gitlab.com/a/b", "https://github.com/a", "xhttps://github.com/a/b"} {
		if _, _, ok := ParseGitHubRemote(bad); ok {
			t.Errorf("%s should not parse", bad)
		}
	}
}

func TestCountTextLines(t *testing.T) {
	cases := map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "a\x00b\n": 0}
	for in, want := range cases {
		if got := CountTextLines(strings.NewReader(in)); got != want {
			t.Errorf("CountTextLines(%q) = %d want %d", in, got, want)
		}
	}
}

func TestParseGitFile(t *testing.T) {
	if p, ok := parseGitFile("gitdir: /home/user/workspace/.git/worktrees/abc\n"); !ok || p != "/home/user/workspace/.git/worktrees/abc" {
		t.Errorf("got %q %v", p, ok)
	}
	if _, ok := parseGitFile("nonsense"); ok {
		t.Error("expected no gitdir")
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setupRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(dir, "ws")
	git(t, dir, "init", "-q", ws)
	write(t, filepath.Join(ws, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(ws, "src", "b.txt"), "b\n")
	git(t, ws, "add", "-A")
	git(t, ws, "commit", "-qm", "init")
	return ws
}

func TestHostRepoOps(t *testing.T) {
	ws := setupRepo(t)
	ctx := context.Background()
	write(t, filepath.Join(ws, "a.txt"), "one\nTWO\nthree\n")
	write(t, filepath.Join(ws, "src", "new.txt"), "x\ny")
	sb := &sandbox.Sandbox{Kind: sandbox.KindHost, Root: ws, WorkspacePath: ws}

	r, err := OpenHost(sb, "")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := DiffStats(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if !stats.IsGitRepo || stats.HeadSHA == nil || stats.Uncommitted != (DiffTotals{Additions: 4, Deletions: 1, FilesChanged: 2}) {
		t.Errorf("stats = %+v", stats)
	}
	diff, err := Diff(ctx, r, "all", false)
	if err != nil || !diff.HasChanges || !strings.Contains(diff.Diff, "+TWO") || !strings.Contains(diff.Diff, "b/src/new.txt") {
		t.Errorf("diff = %+v, %v", diff, err)
	}
	branch, _ := Diff(ctx, r, "branch", false)
	if branch.Error != nil || branch.HasChanges {
		t.Errorf("branch diff on main = %+v", branch)
	}

	sub, err := OpenHost(sb, "src")
	if err != nil {
		t.Fatal(err)
	}
	cp, err := ChangedPaths(ctx, sub)
	if err != nil || !slices.Equal(cp.Paths, []string{"new.txt"}) {
		t.Errorf("changed paths in src = %+v %v", cp, err)
	}
	base, err := FileBaseline(ctx, sub, "b.txt")
	if err != nil || base.Content != "b\n" || !base.IsGitRepo {
		t.Errorf("baseline = %+v %v", base, err)
	}
	missing, _ := FileBaseline(ctx, sub, "new.txt")
	if missing.Content != "" || !missing.IsGitRepo {
		t.Errorf("baseline of new file = %+v", missing)
	}
	br, err := Branches(ctx, r)
	if err != nil || br.CurrentBranch != "main" || !slices.Equal(br.Branches, []string{"main"}) {
		t.Errorf("branches = %+v %v", br, err)
	}
	if _, err := OpenHost(sb, "nope"); err == nil {
		t.Error("missing dir should fail")
	}

	plain := t.TempDir()
	psb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/workspace", WorkspacePath: plain}
	pr, err := OpenHost(psb, "")
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := Diff(ctx, pr, "all", false); d.IsGitRepo {
		t.Error("plain dir reported as repo")
	}
}

// A worktree created inside a Docker sandbox records a container gitdir; the
// host must translate it.
func TestContainerWorktreeTranslation(t *testing.T) {
	ws := setupRepo(t)
	ctx := context.Background()
	git(t, ws, "worktree", "add", "-q", ".worktrees/abcd1234", "-b", "worktree-abcd1234")
	wt := filepath.Join(ws, ".worktrees", "abcd1234")
	write(t, filepath.Join(wt, ".git"), "gitdir: /home/user/workspace/.git/worktrees/abcd1234\n")
	write(t, filepath.Join(wt, "a.txt"), "changed\n")

	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/workspace", WorkspacePath: ws}
	r, err := OpenHost(sb, ".worktrees/abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	if r.WorkTree != wt {
		t.Fatalf("work tree = %q want %q", r.WorkTree, wt)
	}
	br, err := Branches(ctx, r)
	if err != nil || br.CurrentBranch != "worktree-abcd1234" || !br.IsGitRepo {
		t.Errorf("branches = %+v %v", br, err)
	}
	cp, err := ChangedPaths(ctx, r)
	if err != nil || !slices.Equal(cp.Paths, []string{"a.txt"}) {
		t.Errorf("changed = %+v %v", cp, err)
	}
	stats, err := DiffStats(ctx, r)
	if err != nil || stats.Uncommitted.FilesChanged != 1 {
		t.Errorf("stats = %+v %v", stats, err)
	}
}

func TestRestoreScript(t *testing.T) {
	s := RestoreScript("it's.txt", "")
	if !strings.Contains(s, `'it'\''s.txt'`) {
		t.Errorf("unquoted: %s", s)
	}
	if r := RestoreScript("new.txt", "old.txt"); !strings.Contains(r, "git checkout HEAD -- 'old.txt' && {") {
		t.Errorf("rename script: %s", r)
	}
}
