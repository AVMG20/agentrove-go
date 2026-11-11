package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPruneHomeKeepsLoginsAndSettings(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"claude/.credentials.json", ".gitconfig", ".ssh/id_ed25519", ".config/gh/hosts.yml",
		"custom-claude/x", ".cargo/bin/cargo", ".rustup/toolchains/x", "go/pkg/mod/x", ".cache/pip/x", ".npm/x", "notes.txt"} {
		path := filepath.Join(home, p)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneHome(home, []string{"custom-claude"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	want := []string{".config", ".gitconfig", ".ssh", "claude", "custom-claude"}
	if !slices.Equal(left, want) {
		t.Fatalf("left %v, want %v", left, want)
	}
	if err := pruneHome(filepath.Join(home, "missing"), nil); err != nil {
		t.Fatalf("missing home: %v", err)
	}
}

func TestFilesMissing(t *testing.T) {
	dir := t.TempDir()
	if !filesMissing(filepath.Join(dir, "gone")) {
		t.Fatal("a missing dir needs restoring")
	}
	if filesMissing(dir) {
		t.Fatal("an empty project is there")
	}
}
