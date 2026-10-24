package gitops

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// sh runs a checkpoint script locally, as the box would.
func sh(t *testing.T, dir, script string, args ...string) string {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"-c", script, "checkpoint"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}
	return string(out)
}

// tree maps every file outside .git to its content.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" || d.Name() == ".worktrees" {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// A checkpoint captures tracked edits, deletions and untracked files (text
// and binary) without touching the real index, and restoring it puts the
// tree back exactly, in the main checkout and in a worktree (.git is a file).
func TestCheckpointRoundTrip(t *testing.T) {
	ws := setupRepo(t)
	wt := filepath.Join(ws, ".worktrees", "c1")
	write(t, filepath.Join(ws, ".git", "info", "exclude"), ".worktrees/\n") // as CreateWorktree does
	git(t, ws, "worktree", "add", "-q", "-b", "c1", wt)
	for name, dir := range map[string]string{"main": ws, "worktree": wt} {
		t.Run(name, func(t *testing.T) {
			write(t, filepath.Join(dir, "a.txt"), "one\nchanged\n")
			if err := os.Remove(filepath.Join(dir, "src", "b.txt")); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, "new dir", "u.txt"), "untracked\n")
			write(t, filepath.Join(dir, "bin.dat"), "\x00\x01\x02\xff")
			write(t, filepath.Join(dir, "staged.txt"), "staged\n")
			git(t, dir, "add", "staged.txt")
			status := git(t, dir, "status", "--porcelain")
			want := tree(t, dir)
			head := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))

			diff := sh(t, dir, checkpointDiffScript(CheckpointMaxBytes))
			if got := git(t, dir, "status", "--porcelain"); got != status {
				t.Fatalf("capture changed the index:\n%s\nwant\n%s", got, status)
			}
			if len(sh(t, dir, checkpointDiffScript(10))) != 11 {
				t.Fatal("size cap not applied")
			}

			// The run makes more changes, then the user restores.
			write(t, filepath.Join(dir, "a.txt"), "rewritten\n")
			write(t, filepath.Join(dir, "agent.txt"), "agent\n")
			git(t, dir, "add", "-A")
			git(t, dir, "commit", "-qm", "agent")
			write(t, filepath.Join(dir, "later.txt"), "x\n")

			patch := filepath.Join(t.TempDir(), "p.patch")
			if err := os.WriteFile(patch, []byte(diff), 0o644); err != nil {
				t.Fatal(err)
			}
			sh(t, dir, restoreCheckpointScript("", head), patch)
			if got := tree(t, dir); !reflect.DeepEqual(got, want) {
				t.Fatalf("restored tree = %v\nwant %v", got, want)
			}
		})
	}
}
