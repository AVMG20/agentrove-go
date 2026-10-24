package gitops

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"agentrove/internal/sandbox"

	"github.com/google/uuid"
)

type Checkpoint struct {
	BaseHead   string
	PreRunDiff string
}

// One round-trip so a clean tree (the common case) skips the diff capture.
const checkpointProbe = `git rev-parse HEAD 2>/dev/null && (test -z "$(git status --porcelain 2>/dev/null)" && echo clean || echo dirty)`

// CheckpointMaxBytes caps a turn's pre-run diff; a tree dirtier than that
// (say a large untracked file) gets no checkpoint rather than a huge row.
const CheckpointMaxBytes = 20 << 20

// ErrCheckpointTooLarge means the uncommitted changes exceed CheckpointMaxBytes.
var ErrCheckpointTooLarge = errors.New("uncommitted changes are too large to checkpoint")

// checkpointDiffScript prints the whole tree's uncommitted diff, untracked
// files included, in one git process: intent-to-add entries in a throwaway
// copy of the index (the real one, and what's staged, stay untouched) make
// `git diff HEAD` show untracked files as new. At most max+1 bytes come back.
func checkpointDiffScript(max int) string {
	return `T=$(mktemp) || exit 1; { cp "$(git rev-parse --git-path index)" "$T" 2>/dev/null || rm -f "$T"; }; ` +
		`GIT_INDEX_FILE="$T" git add -A -N --ignore-errors -- :/ >/dev/null 2>&1; ` +
		`GIT_INDEX_FILE="$T" git diff --binary HEAD 2>/dev/null | head -c ` + strconv.Itoa(max+1) + `; rm -f "$T"`
}

func cdPrefix(cwd string) string {
	if cwd == "" {
		return ""
	}
	return "cd " + shellQuote(cwd) + " && "
}

// CreateCheckpoint records HEAD plus the uncommitted diff before a turn, so
// "restore" can put the tree back exactly. Returns nil for non-git workspaces.
func CreateCheckpoint(ctx context.Context, sb *sandbox.Sandbox, cwd string) (*Checkpoint, error) {
	res, err := sb.Shell(ctx, "", cdPrefix(cwd)+checkpointProbe, 30*time.Second)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, nil
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}
	cp := &Checkpoint{BaseHead: strings.TrimSpace(lines[0])}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) == "clean" {
		return cp, nil
	}
	diff, err := sb.Shell(ctx, "", cdPrefix(cwd)+checkpointDiffScript(CheckpointMaxBytes), time.Minute)
	if err != nil {
		return nil, err
	}
	if len(diff.Stdout) > CheckpointMaxBytes {
		return nil, ErrCheckpointTooLarge
	}
	cp.PreRunDiff = diff.Stdout
	return cp, nil
}

var shaRE = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// RestoreCheckpoint resets to base_head, cleans, and re-applies the pre-run diff.
func RestoreCheckpoint(ctx context.Context, sb *sandbox.Sandbox, cwd, baseHead, preRunDiff string) (sandbox.ExecResult, error) {
	if !shaRE.MatchString(baseHead) {
		return sandbox.ExecResult{}, fmt.Errorf("Invalid checkpoint commit")
	}
	patch := ""
	if preRunDiff != "" {
		patch = "/tmp/.agentrove-checkpoint-" + uuid.NewString() + ".patch"
		res, err := sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"sh", "-c", `cat > "$0"`, patch}, Stdin: strings.NewReader(preRunDiff), Timeout: 30 * time.Second})
		if err != nil {
			return res, err
		}
		if res.ExitCode != 0 {
			return res, fmt.Errorf("write checkpoint patch: %s", res.Output())
		}
	}
	return sb.Exec(ctx, sandbox.ExecRequest{Cmd: []string{"bash", "-c", restoreCheckpointScript(cwd, baseHead), "restore", patch}, Timeout: 2 * time.Minute})
}

// restoreCheckpointScript takes the patch file as $1 (empty: none).
func restoreCheckpointScript(cwd, baseHead string) string {
	return cdPrefix(cwd) + `cd "$(git rev-parse --show-toplevel)" && ` +
		`git reset --hard ` + shellQuote(baseHead) + ` >/dev/null 2>&1; status=$?; ` +
		`if [ $status -eq 0 ]; then git clean -fd >/dev/null 2>&1; status=$?; fi; ` +
		`if [ $status -eq 0 ] && [ -n "$1" ]; then git apply --whitespace=nowarn "$1" 2>&1; status=$?; fi; ` +
		`[ -n "$1" ] && rm -f "$1"; exit $status`
}
