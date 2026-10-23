package chat

import (
	"os"
	"path/filepath"
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
	if err := os.Symlink(victim, filepath.Join(ws, "abc.txt")); err != nil {
		t.Fatal(err)
	}
	m := &Module{a: &app.App{Cfg: &config.Config{StoragePath: storage}}}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/proj", WorkspacePath: ws}
	name := "notes.txt"
	req := &turnRequest{prompt: "hi", attachments: []models.Attachment{{FilePath: &rel, FileType: "file", Filename: &name}}}
	if _, err := m.promptBlocks(sb, req); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "DB" {
		t.Fatalf("attachment copy followed a planted symlink; host file is now %q", b)
	}
	if b, err := os.ReadFile(filepath.Join(ws, "abc.txt")); err != nil || string(b) != "upload" {
		t.Errorf("attachment in workspace = %q %v", b, err)
	}
}
