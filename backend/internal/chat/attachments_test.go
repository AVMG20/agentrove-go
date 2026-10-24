package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"agentrove/internal/app"
	"agentrove/internal/config"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// Copying a non-native attachment into the box-writable workspace must not
// follow a symlink the agent planted at the destination name.
func TestPromptBlocksAttachmentDoesNotFollowSymlink(t *testing.T) {
	storage := t.TempDir()
	rel := "temp/u1/files/abc.txt"
	if err := os.MkdirAll(filepath.Join(storage, "temp", "u1", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, filepath.FromSlash(rel)), []byte("upload"), 0o644); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(storage, "agentrove.db")
	if err := os.WriteFile(victim, []byte("DB"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(storage, "workspaces", "u1", "proj")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	uploads := filepath.Join(ws, filepath.FromSlash(uploadsDir))
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(uploads, "abc.txt")); err != nil {
		t.Fatal(err)
	}
	m := &Module{a: &app.App{Cfg: &config.Config{StoragePath: storage}}}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/proj", WorkspacePath: ws}
	name := "notes.txt"
	req := &turnRequest{chat: &models.Chat{}, prompt: "hi", attachments: []models.Attachment{{FilePath: &rel, FileType: "file", Filename: &name}}}
	if _, err := m.promptBlocks(sb, req); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "DB" {
		t.Fatalf("attachment copy followed a planted symlink; host file is now %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(uploads, "abc.txt")); err != nil || string(b) != "upload" {
		t.Errorf("attachment in workspace = %q %v", b, err)
	}
}

// Attachments land in the chat's working dir (its worktree, if any), where
// the agent reads them, and never get committed.
func TestPromptBlocksAttachmentIsGitIgnored(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	storage := t.TempDir()
	rel := "temp/u1/files/abc.csv"
	if err := os.MkdirAll(filepath.Join(storage, "temp", "u1", "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, filepath.FromSlash(rel)), []byte("a,b"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(storage, "workspaces", "u1", "proj")
	wt := filepath.Join(ws, ".worktrees", "c1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = ws
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	m := &Module{a: &app.App{Cfg: &config.Config{StoragePath: storage}}}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/proj", WorkspacePath: ws}
	name := "data.csv"
	cwd := ".worktrees/c1"
	req := &turnRequest{chat: &models.Chat{WorktreeCwd: &cwd}, prompt: "hi",
		attachments: []models.Attachment{{FilePath: &rel, FileType: "file", Filename: &name}}}
	blocks, err := m.promptBlocks(sb, req)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(wt, ".agentrove", "uploads", "abc.csv")); err != nil || string(b) != "a,b" {
		t.Fatalf("attachment in the worktree = %q %v", b, err)
	}
	note, _ := blocks[len(blocks)-1]["text"].(string)
	if !strings.Contains(note, "/home/user/projects/proj/.worktrees/c1/.agentrove/uploads/abc.csv") {
		t.Errorf("note = %q", note)
	}
	cmd = exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = ws
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "agentrove") {
		t.Fatalf("attachment visible to git add -A:\n%s", out)
	}
}
