package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	if _, err := s.Files("claude", "../etc", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("traversal name: %v", err)
	}
	if _, err := s.Files("codex", "mine", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("other source: %v", err)
	}
	files, err := s.Files("claude", "mine", "")
	if err != nil || len(files) != 1 || files[0].Path != "SKILL.md" {
		t.Fatalf("files = %+v %v", files, err)
	}

	if _, err := s.Update("claude", "mine", "", []FileEntry{{Path: "../x", Content: "x"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("escaping path: %v", err)
	}
	if _, err := s.Update("claude", "mine", "", []FileEntry{{Path: "SKILL.md", Content: "no frontmatter"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad frontmatter: %v", err)
	}
	out, err := s.Update("claude", "mine", "", []FileEntry{
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

// The skill dirs are written by the agent inside the box; symlinks and the
// plugin manifest must not make the API read or delete other host files.
func TestSkillsStayInsideTheirRoots(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{StoragePath: storage}
	ws := &models.Workspace{ID: "w", UserID: "u1", SandboxProvider: "docker", WorkspacePath: filepath.Join(storage, "ws")}
	claude := ClaudeConfigDir(cfg, ws)
	skills := filepath.Join(ws.WorkspacePath, ".claude", "skills")

	// Another user's data on the same host.
	victim := filepath.Join(storage, "workspaces", "u2", "proj")
	writeSkill(t, victim, "stolen", "other user's skill")
	secret := filepath.Join(storage, "secret.txt")
	if err := os.WriteFile(secret, []byte("---\ndescription: top secret\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	writeSkill(t, skills, "ok", "fine")
	// A file inside the skill linking out.
	if err := os.Symlink(secret, filepath.Join(skills, "ok", "leak.md")); err != nil {
		t.Fatal(err)
	}
	// A skill whose SKILL.md links out.
	if err := os.MkdirAll(filepath.Join(skills, "linkmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(skills, "linkmd", skillMD)); err != nil {
		t.Fatal(err)
	}
	// A skill dir linking to another tree that holds a SKILL.md.
	if err := os.Symlink(filepath.Join(victim, "stolen"), filepath.Join(skills, "stolen")); err != nil {
		t.Fatal(err)
	}
	// A plugin whose recorded install path is a host dir outside the config dir.
	_ = os.MkdirAll(filepath.Join(claude, "plugins"), 0o755)
	_ = os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"enabledPlugins":{"p@m":true}}`), 0o644)
	_ = os.WriteFile(filepath.Join(claude, "plugins", "installed_plugins.json"),
		[]byte(`{"plugins":{"p@m":[{"installPath":"`+filepath.Dir(victim)+`"}]}}`), 0o644)
	writeSkill(t, filepath.Join(filepath.Dir(victim), "skills"), "viaplugin", "other user's plugin skill")

	s := NewService(cfg, ws)
	for _, sk := range s.List() {
		if sk.Name != "ok" {
			t.Errorf("listed %q (%s) from outside its root", sk.Name, sk.Description)
		}
	}
	files, err := s.Files("claude", "ok", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if f.Path == "leak.md" {
			t.Errorf("followed a symlink out of the skill: %q", f.Content)
		}
	}
	for _, name := range []string{"stolen", "linkmd", "viaplugin"} {
		if _, err := s.Files("claude", name, ""); !errors.Is(err, ErrNotFound) {
			t.Errorf("Files(%s) = %v, want not found", name, err)
		}
		if _, err := s.Update("claude", name, "", []FileEntry{{Path: "SKILL.md", Content: "---\ndescription: x\n---\n"}}); !errors.Is(err, ErrNotFound) {
			t.Errorf("Update(%s) = %v, want not found", name, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(victim, "stolen", skillMD)); err != nil || !strings.Contains(string(b), "other user's skill") {
		t.Errorf("victim skill was modified: %q %v", b, err)
	}
}

// A ~/claude swapped for a symlink out of HOME must not let skill reads/writes
// escape: os.Root is anchored at the (non-swappable) HOME mount, above claude.
func TestConfigDirSymlinkSwapBlocked(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{StoragePath: storage}
	ws := &models.Workspace{ID: "w", UserID: "u1", SandboxProvider: "docker", WorkspacePath: filepath.Join(storage, "workspaces", "u1", "proj")}
	if err := os.MkdirAll(ws.WorkspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	home := cfg.AgentHomeDir("u1")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	// Another user's config on the same host, with a real skill.
	victimClaude := filepath.Join(storage, "agent-homes", "u2", "claude")
	writeSkill(t, filepath.Join(victimClaude, "skills"), "mine", "other user's skill")
	// The agent replaces its own ~/claude with a symlink to the victim's.
	if err := os.Symlink(victimClaude, filepath.Join(home, "claude")); err != nil {
		t.Fatal(err)
	}

	s := NewService(cfg, ws)
	if list := s.List(); len(list) != 0 {
		t.Errorf("listed skills through a swapped ~/claude: %+v", list)
	}
	if _, err := s.Files("claude", "mine", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("Files via swapped ~/claude = %v, want not found", err)
	}
	if _, err := s.Update("claude", "mine", "", []FileEntry{{Path: "SKILL.md", Content: "---\ndescription: x\n---\n"}}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update via swapped ~/claude = %v, want not found", err)
	}
	if b, err := os.ReadFile(filepath.Join(victimClaude, "skills", "mine", skillMD)); err != nil || !strings.Contains(string(b), "other user's skill") {
		t.Errorf("victim skill was modified: %q %v", b, err)
	}
}

func TestCreateDeleteAndScopes(t *testing.T) {
	storage := t.TempDir()
	cfg := &config.Config{StoragePath: storage}
	ws := &models.Workspace{ID: "w", UserID: "u1", SandboxProvider: "docker", WorkspacePath: filepath.Join(storage, "ws")}
	if err := os.MkdirAll(ws.WorkspacePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.AgentHomeDir("u1"), 0o755); err != nil {
		t.Fatal(err)
	}
	md := []FileEntry{{Path: "SKILL.md", Content: "---\nname: x\ndescription: d\n---\nbody"}}

	// Global skills need no project, and the skills dir is created on demand.
	user := NewUserService(cfg, "u1")
	if out, err := user.Create("claude", "global-one", ScopeUser, md); err != nil || out.Scope != ScopeUser {
		t.Fatalf("create user: %+v %v", out, err)
	}
	if _, err := user.Create("claude", "global-one", ScopeUser, md); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	// A folder that isn't a valid skill yet (no SKILL.md) is never replaced.
	half := filepath.Join(cfg.AgentHomeDir("u1"), "claude", "skills", "half")
	if err := os.MkdirAll(half, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(half, "run.sh"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := user.Create("claude", "half", ScopeUser, md); !errors.Is(err, ErrConflict) {
		t.Fatalf("existing folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(half, "run.sh")); err != nil {
		t.Fatalf("existing folder was touched: %v", err)
	}
	for _, bad := range []string{"Bad Name", "../x", "", strings.Repeat("a", 65)} {
		if _, err := user.Create("claude", bad, ScopeUser, md); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", bad, err)
		}
	}
	if _, err := user.Create("claude", "p", ScopeProject, md); !errors.Is(err, ErrInvalid) {
		t.Errorf("project scope without a project: %v", err)
	}

	proj := NewService(cfg, ws)
	if _, err := proj.Create("claude", "global-one", ScopeProject, md); err != nil {
		t.Fatalf("same name in project scope: %v", err)
	}
	scopes := map[string]string{}
	for _, sk := range proj.List() {
		scopes[sk.Name] = sk.Scope
	}
	// The project copy shadows the global one in a project's list.
	if scopes["global-one"] != ScopeProject {
		t.Fatalf("scopes: %v", scopes)
	}
	if err := proj.Delete("claude", "global-one", ScopeUser); err != nil {
		t.Fatalf("delete user copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws.WorkspacePath, ".claude", "skills", "global-one", "SKILL.md")); err != nil {
		t.Fatalf("project copy must stay: %v", err)
	}
	if list := user.List(); len(list) != 0 {
		t.Fatalf("user list after delete: %v", list)
	}
	if err := user.Delete("claude", "missing", ScopeUser); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}
