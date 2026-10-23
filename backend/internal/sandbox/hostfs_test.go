package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteWorkspaceFileDoesNotFollowSymlinks(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(parent, "proj")
	if err := os.Mkdir(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "host-secret.db")
	if err := os.WriteFile(outside, []byte("DB"), 0o600); err != nil {
		t.Fatal(err)
	}
	sb := &Sandbox{Kind: KindDocker, Root: "/home/user/projects/proj", WorkspacePath: ws}

	// A symlink planted at the target name is replaced, not written through.
	if err := os.Symlink(outside, filepath.Join(ws, "upload.txt")); err != nil {
		t.Fatal(err)
	}
	if err := sb.WriteWorkspaceFile("upload.txt", []byte("attachment")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "DB" {
		t.Fatalf("write followed a planted symlink: host file is now %q", b)
	}
	fi, err := os.Lstat(filepath.Join(ws, "upload.txt"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("target should be a regular file now: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(ws, "upload.txt")); string(b) != "attachment" {
		t.Errorf("content = %q", b)
	}
	if ents, _ := os.ReadDir(ws); len(ents) != 1 {
		t.Errorf("temp file left behind: %v", ents)
	}

	// A project dir swapped for a symlink out of the user's tree is refused.
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), ws); err != nil {
		t.Fatal(err)
	}
	if err := sb.WriteWorkspaceFile("planted.txt", []byte("x")); err == nil {
		t.Error("write through a swapped project dir must fail")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(outside), "planted.txt")); err == nil {
		t.Error("write escaped through the swapped project dir")
	}
}

func TestReadRegularRefusesSymlinksAndCaps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if b, err := ReadRegular(root, "a", 10); err != nil || string(b) != "hello" {
		t.Errorf("regular read = %q %v", b, err)
	}
	if _, err := ReadRegular(root, "link", 10); err == nil {
		t.Error("ReadRegular must refuse a symlink, even one inside the root")
	}
	if _, err := ReadRegular(root, "a", 3); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("size cap: %v", err)
	}
}
