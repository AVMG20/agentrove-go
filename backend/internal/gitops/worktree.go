package gitops

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"agentrove/internal/sandbox"
)

var baseRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)

// ValidBaseRef rejects refs that could smuggle shell or revision syntax.
func ValidBaseRef(ref string) bool {
	return baseRefRE.MatchString(ref) && !strings.Contains(ref, "..") && !strings.Contains(ref, "//") &&
		!strings.Contains(ref, "@{") && !strings.HasSuffix(ref, "/") && !strings.HasSuffix(ref, ".lock")
}

// ChatWorktreePath is the workspace-relative worktree dir of a chat. Comparing
// a chat's worktree_cwd to it doubles as the ownership test on deletion (a
// sub-thread inherits its parent's cwd, which won't match its own id).
func ChatWorktreePath(chatID string) string {
	short := chatID
	if len(short) > 8 {
		short = short[:8]
	}
	return path.Join(".worktrees", short)
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// worktreeAddScript is idempotent (an earlier attempt may have created the dir
// but failed to persist it) and hides .worktrees/ from the parent checkout via
// info/exclude. --relative-paths (git >= 2.48) keeps the worktree's .git link
// valid both inside the container and from the host; older git falls back.
func worktreeAddScript(dir, branch, base string) string {
	q := shellQuote
	pre := `git rev-parse --is-inside-work-tree >/dev/null 2>&1 || exit 2; ` +
		`if [ -e ` + q(dir+"/.git") + ` ]; then echo exists; exit 0; fi; ` +
		`excl="$(git rev-parse --git-path info/exclude)"; ` +
		`{ mkdir -p "${excl%/*}" && grep -qxF ".worktrees/" "$excl" || echo ".worktrees/" >> "$excl"; } 2>/dev/null; ` +
		`mkdir -p .worktrees && `
	baseArg := ""
	if base != "" {
		baseArg = " " + q(base)
	}
	add := "git worktree add %s " + q(dir) + " -b " + q(branch) + baseArg + " 2>&1"
	return pre + "{ " + fmt.Sprintf(add, "--relative-paths") + " || " + fmt.Sprintf(add, "") + "; }"
}

// CreateWorktree creates the chat's worktree at the workspace root and returns
// its workspace-relative path. Failures surface: the user asked for isolation.
func CreateWorktree(ctx context.Context, sb *sandbox.Sandbox, chatID, baseRef string) (string, error) {
	if baseRef != "" && !ValidBaseRef(baseRef) {
		return "", fmt.Errorf("Invalid base branch name")
	}
	dir := ChatWorktreePath(chatID)
	branch := "worktree-" + path.Base(dir)
	res, err := sb.Shell(ctx, "", worktreeAddScript(dir, branch, baseRef), time.Minute)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 && baseRef != "" && !strings.HasPrefix(baseRef, "origin/") {
		res, err = sb.Shell(ctx, "", worktreeAddScript(dir, branch, "origin/"+baseRef), time.Minute)
		if err != nil {
			return "", err
		}
	}
	if res.ExitCode == 0 {
		return dir, nil
	}
	msg := strings.TrimSpace(res.Output())
	if res.ExitCode == 2 || msg == "" {
		msg = "Worktree mode requires a git workspace"
	}
	return "", fmt.Errorf("%s", msg)
}

// RemoveWorktree is best-effort: without --force git refuses dirty worktrees
// and unmerged branches, so no work is ever destroyed.
func RemoveWorktree(ctx context.Context, sb *sandbox.Sandbox, worktreeCwd string) {
	branch := "worktree-" + path.Base(worktreeCwd)
	script := "export GIT_DISCOVERY_ACROSS_FILESYSTEM=1 && git worktree remove " + shellQuote(worktreeCwd) +
		" 2>&1 && git branch -d " + shellQuote(branch) + " 2>&1"
	_, _ = sb.Shell(ctx, "", script, time.Minute)
}
