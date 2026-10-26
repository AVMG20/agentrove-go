package sandboxapi

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"agentrove/internal/sandbox"
)

// Links to files outside the project (an agent's own dirs under HOME) open
// read-only from the box HOME; nothing outside HOME is reachable, symlinks
// included.
func TestReadBoxFile(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	projects, _ := filepath.EvalSymlinks(t.TempDir())
	ws := filepath.Join(projects, "proj")
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	brain := filepath.Join(home, ".gemini", "brain")
	for _, d := range []string{ws, brain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(brain, "page.html"), "<p>hi</p>")
	write(filepath.Join(ws, "a.txt"), "in project")
	write(filepath.Join(outside, "pw"), "secret")
	if err := os.Symlink(filepath.Join(outside, "pw"), filepath.Join(brain, "leak")); err != nil {
		t.Fatal(err)
	}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/proj", Home: "/home/user", WorkspacePath: ws}

	fc, err := readBoxFile(sb, home, "/home/user/.gemini/brain/page.html")
	if err != nil || fc.Content != "<p>hi</p>" || fc.Path != "/home/user/.gemini/brain/page.html" {
		t.Fatalf("home file = %+v, %v", fc, err)
	}
	if fc, err := readBoxFile(sb, home, "/home/user/projects/proj/a.txt"); err != nil || fc.Content != "in project" {
		t.Fatalf("project file = %+v, %v", fc, err)
	}
	if _, err := readBoxFile(sb, home, "/home/user/.gemini/brain/leak"); err == nil {
		t.Fatal("followed a symlink out of HOME")
	}
	if _, err := readBoxFile(sb, home, "/etc/passwd"); !errors.Is(err, errOutsideHome) {
		t.Fatalf("outside HOME: %v", err)
	}
	if _, err := readBoxFile(sb, home, "/home/user/../../etc/passwd"); !errors.Is(err, errOutsideHome) {
		t.Fatalf("dot-dot: %v", err)
	}
	if _, err := readBoxFile(sb, home, "relative.txt"); !errors.Is(err, errNotAbsolute) {
		t.Fatalf("relative: %v", err)
	}
	if _, err := readBoxFile(sb, home, "/home/user/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}
