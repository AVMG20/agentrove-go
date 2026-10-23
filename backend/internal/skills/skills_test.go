package skills

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"agentrove/internal/config"
	"agentrove/internal/models"
)

func TestParseFrontmatter(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "---\nname: x\ndescription: Does things\n---\nbody", "Does things"},
		{"colon in value", "---\ndescription: Use when: deploying\n---\n", "Use when: deploying"},
		{"double quoted", "---\ndescription: \"Say \\\"hi\\\"\"\n---\n", `Say "hi"`},
		{"single quoted", "---\ndescription: 'it''s'\n---\n", "it's"},
		{"literal block", "---\ndescription: |\n  line one\n  line two\nname: x\n---\n", "line one\nline two\n"},
		{"literal strip", "---\ndescription: |-\n  a\n  b\n---\n", "a\nb"},
		{"folded", "---\ndescription: >\n  a\n  b\n\n  c\n---\n", "a b\nc\n"},
		{"continuation", "---\ndescription: first\n  second\nname: x\n---\n", "first\nsecond"},
		{"comment", "---\ndescription: hello # note\n---\n", "hello"},
		{"missing", "---\nname: x\n---\n", ""},
	}
	for _, c := range cases {
		meta, err := ParseFrontmatter(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if meta["description"] != c.want {
			t.Errorf("%s: description = %q want %q", c.name, meta["description"], c.want)
		}
	}
	for _, bad := range []string{"no frontmatter", "---\nname: x\n"} {
		if _, err := ParseFrontmatter(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func writeSkill(t *testing.T, dir, name, desc string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, skillMD), []byte("---\nname: "+name+"\ndescription: "+desc+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestServiceListFilesUpdate(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{StoragePath: storage}
	ws := &models.Workspace{ID: "w", UserID: "u1", SandboxProvider: "docker", WorkspacePath: filepath.Join(storage, "ws")}
	claude := ClaudeConfigDir(cfg, ws)
	if claude != filepath.Join(storage, "agent-homes", "u1", "claude") {
		t.Fatalf("claude dir = %s", claude)
	}
	writeSkill(t, filepath.Join(ws.WorkspacePath, ".claude", "skills"), "shared", "workspace version")
	writeSkill(t, filepath.Join(claude, "skills"), "shared", "user version")
	writeSkill(t, filepath.Join(claude, "skills"), "mine", "user skill")
	// A plugin recorded with its in-container install path.
	pluginDir := filepath.Join(claude, "plugins", "cache", "p1")
	writeSkill(t, filepath.Join(pluginDir, "skills"), "plug", "from plugin")
	_ = os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"enabledPlugins":{"p1@m":true,"p2@m":false}}`), 0o644)
	_ = os.WriteFile(filepath.Join(claude, "plugins", "installed_plugins.json"),
		[]byte(`{"plugins":{"p1@m":[{"installPath":"/home/user/.claude/plugins/cache/p1"}],"p2@m":[{"installPath":"/nope"}]}}`), 0o644)

	s := NewService(cfg, ws)
	list := s.List()
	got := map[string]string{}
	for _, sk := range list {
		got[sk.Name] = sk.Description
		if len(sk.Sources) != 1 || sk.Sources[0] != "claude" || sk.FileCount != 1 {
			t.Errorf("skill %+v", sk)
		}
	}
	want := map[string]string{"shared": "workspace version", "mine": "user skill", "plug": "from plugin"}
	if len(got) != len(want) {
		t.Fatalf("list = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q want %q", k, got[k], v)
		}
	}

	if _, err := s.Files("claude", "../etc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("traversal name: %v", err)
	}
	if _, err := s.Files("codex", "mine"); !errors.Is(err, ErrNotFound) {
		t.Errorf("other source: %v", err)
	}
	files, err := s.Files("claude", "mine")
	if err != nil || len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("files = %+v %v", files, err)
	}

	if _, err := s.Update("claude", "mine", []FileEntry{{Path: "../x", Content: "x"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("escaping path: %v", err)
	}
	if _, err := s.Update("claude", "mine", []FileEntry{{Path: "SKILL.md", Content: "no frontmatter"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad frontmatter: %v", err)
	}
	out, err := s.Update("claude", "mine", []FileEntry{
		{Path: "SKILL.md", Content: "---\ndescription: new\n---\n"},
		{Path: "refs/data.bin", Content: "AAE=", IsBinary: true},
	})
	if err != nil || out.Description != "new" || out.FileCount != 2 {
		t.Fatalf("update = %+v %v", out, err)
	}
	b, err := os.ReadFile(filepath.Join(claude, "skills", "mine", "refs", "data.bin"))
	if err != nil || len(b) != 2 || b[1] != 1 {
		t.Errorf("binary file = %v %v", b, err)
	}
}
