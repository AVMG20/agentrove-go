// Package skills lists and edits Claude skills (<dir>/<name>/SKILL.md) from
// the workspace's .claude/skills, the user's Claude config dir and enabled
// Claude plugins, and serves /api/v1/skills.
package skills

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

	"agentrove/internal/config"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

const (
	// Source is the only agent kind skills are scanned for.
	Source  = "claude"
	skillMD = "SKILL.md"
)

type CustomSkill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	SizeBytes   int64    `json:"size_bytes"`
	FileCount   int      `json:"file_count"`
	Sources     []string `json:"sources"`
	ReadOnly    bool     `json:"read_only"`
}

type FileEntry struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	IsBinary bool   `json:"is_binary"`
}

// ErrNotFound and ErrInvalid map to 404 / 400.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
)

// ClaudeConfigDir is the host path of the per-user Claude config dir, seen
// in the box as /home/user/claude (CLAUDE_CONFIG_DIR).
func ClaudeConfigDir(cfg *config.Config, ws *models.Workspace) string {
	return filepath.Join(cfg.AgentHomeDir(ws.UserID), "claude")
}

// skillRoot locates a directory of skills in a way that is safe against the
// agent, which is root inside the box and can replace any directory it owns
// (its project dir, its ~/claude) with a symlink pointing at other host files.
// Instead of validating a path and then opening it (a TOCTOU), every access
// goes through os.Root starting at anchor — a bind-mount point the box cannot
// replace — and navigates inward. os.Root refuses any component that is a
// symlink escaping the anchor and pins each directory to an fd, so a swap
// after the check cannot move the operation out of the user's own tree.
//
//	anchor  non-swappable mount dir to open first (per-user projects dir / HOME)
//	pin     a dir under anchor to sub-root before rel (the swappable project
//	        dir), pinning it; "" when rel is reached directly under anchor
//	rel     the skills base dir, relative to the pinned dir (or anchor)
type skillRoot struct {
	anchor, pin, rel string
}

// openBase opens the skills base dir as a pinned, symlink-safe *os.Root. The
// caller closes it.
func (sr skillRoot) openBase() (*os.Root, error) {
	a, err := os.OpenRoot(sr.anchor)
	if err != nil {
		return nil, err
	}
	cur := a
	if sr.pin != "" {
		p, err := a.OpenRoot(sr.pin)
		a.Close()
		if err != nil {
			return nil, err
		}
		cur = p
	}
	base, err := cur.OpenRoot(sr.rel)
	cur.Close()
	return base, err
}

type Service struct {
	paths []skillRoot // search order: workspace first, then user, then plugins
}

// maxSkillFile bounds one file the API reads from an agent-writable skill
// dir; it matches the size Update accepts.
const maxSkillFile = 10_000_000

// readFileLimited reads rel under root (symlink-safe), rejecting non-regular
// or oversized files.
func readFileLimited(root *os.Root, rel string) ([]byte, error) {
	fi, err := root.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	if fi.Size() > maxSkillFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", rel, maxSkillFile)
	}
	return root.ReadFile(rel)
}

func NewService(cfg *config.Config, ws *models.Workspace) *Service {
	claudeDir := ClaudeConfigDir(cfg, ws)
	paths := []skillRoot{
		// Workspace skills: anchor at the (non-swappable) projects mount, pin
		// the project dir to defeat a project-dir symlink swap, then .claude/skills.
		{anchor: filepath.Dir(ws.WorkspacePath), pin: filepath.Base(ws.WorkspacePath), rel: filepath.Join(".claude", "skills")},
		// Config-dir skills: anchor at the (non-swappable) HOME mount; ~/claude
		// itself is swappable, so os.Root navigation guards it.
		{anchor: cfg.AgentHomeDir(ws.UserID), rel: "claude/skills"},
	}
	containerClaude := ""
	if ws.SandboxProvider == sandbox.KindDocker {
		containerClaude = sandbox.ContainerClaudeDir
	}
	paths = append(paths, pluginSkillPaths(cfg.AgentHomeDir(ws.UserID), claudeDir, containerClaude)...)
	return &Service{paths: paths}
}

// pluginSkillPaths cross-references settings.json (enabledPlugins) with
// plugins/installed_plugins.json (install paths). Install paths recorded
// inside a Docker sandbox are mapped back onto the host config dir, and every
// path is anchored at the (non-swappable) HOME mount so a manifest written by
// the agent can't point the API at another user's tree.
func pluginSkillPaths(homeDir, claudeDir, containerClaude string) []skillRoot {
	var settings struct {
		EnabledPlugins map[string]any `json:"enabledPlugins"`
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if !readJSON(filepath.Join(claudeDir, "settings.json"), &settings) ||
		!readJSON(filepath.Join(claudeDir, "plugins", "installed_plugins.json"), &installed) {
		return nil
	}
	ids := make([]string, 0, len(installed.Plugins))
	for id := range installed.Plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []skillRoot
	for _, id := range ids {
		if enabled, _ := settings.EnabledPlugins[id].(bool); !enabled {
			continue
		}
		for _, entry := range installed.Plugins[id] {
			p := entry.InstallPath
			if p == "" {
				continue
			}
			if containerClaude != "" {
				// Installs from before the box layout recorded ~/.claude.
				for _, prefix := range []string{containerClaude, sandbox.ContainerHome + "/.claude"} {
					if rest, ok := strings.CutPrefix(p, prefix); ok {
						p = filepath.Join(claudeDir, filepath.FromSlash(rest))
						break
					}
				}
			}
			// The skills dir must resolve within the user's own HOME mount; a
			// manifest install path pointing elsewhere (another user's dir) is
			// rejected as an escaping relative path.
			rel, err := filepath.Rel(homeDir, filepath.Join(p, "skills"))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				continue
			}
			out = append(out, skillRoot{anchor: homeDir, rel: filepath.ToSlash(rel)})
		}
	}
	return out
}

func readJSON(p string, v any) bool {
	b, err := os.ReadFile(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

// dirStats counts the regular files and total size of the skill dir name,
// following only symlinks that stay inside base.
func dirStats(base *os.Root, name string) (count int, size int64) {
	_ = fs.WalkDir(base.FS(), name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if fi, err := base.Stat(p); err == nil && fi.Mode().IsRegular() {
			count++
			size += fi.Size()
		}
		return nil
	})
	return count, size
}

func description(meta map[string]string) string { return meta["description"] }

// skillExists reports whether name under base is a directory with a readable
// SKILL.md, following only in-root symlinks.
func skillExists(base *os.Root, name string) bool {
	fi, err := base.Stat(name)
	if err != nil || !fi.IsDir() {
		return false
	}
	_, err = readFileLimited(base, path.Join(name, skillMD))
	return err == nil
}

// List returns every skill, deduplicated by name (search order wins).
func (s *Service) List() []CustomSkill {
	out := []CustomSkill{}
	seenNames := map[string]bool{}
	for _, sr := range s.paths {
		base, err := sr.openBase()
		if err != nil {
			continue
		}
		entries, err := fs.ReadDir(base.FS(), ".")
		if err != nil {
			base.Close()
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if seenNames[name] || !skillExists(base, name) {
				continue
			}
			content, err := readFileLimited(base, path.Join(name, skillMD))
			if err != nil || !utf8.Valid(content) {
				continue
			}
			meta, err := ParseFrontmatter(string(content))
			if err != nil {
				meta = map[string]string{}
			}
			count, size := dirStats(base, name)
			seenNames[name] = true
			out = append(out, CustomSkill{Name: name, Description: description(meta), SizeBytes: size, FileCount: count, Sources: []string{Source}})
		}
		base.Close()
	}
	return out
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

// find returns the skillRoot that holds the named skill.
func (s *Service) find(source, name string) (skillRoot, error) {
	if source != Source || !validName(name) {
		return skillRoot{}, fmt.Errorf("%w: Skill '%s' not found", ErrNotFound, name)
	}
	for _, sr := range s.paths {
		base, err := sr.openBase()
		if err != nil {
			continue
		}
		ok := skillExists(base, name)
		base.Close()
		if ok {
			return sr, nil
		}
	}
	return skillRoot{}, fmt.Errorf("%w: Skill '%s' not found", ErrNotFound, name)
}

// Files returns every file of a skill; non-UTF-8 files come back base64.
func (s *Service) Files(source, name string) ([]FileEntry, error) {
	sr, err := s.find(source, name)
	if err != nil {
		return nil, err
	}
	base, err := sr.openBase()
	if err != nil {
		return nil, err
	}
	defer base.Close()
	var paths []string
	_ = fs.WalkDir(base.FS(), name, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Stat follows symlinks, but only to targets inside the skills root.
		if fi, err := base.Stat(p); err == nil && fi.Mode().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	files := []FileEntry{}
	for _, p := range paths {
		rel := strings.TrimPrefix(p, name+"/")
		data, err := readFileLimited(base, p)
		if err != nil {
			if fi, serr := base.Stat(p); serr == nil && fi.Size() > maxSkillFile {
				return nil, fmt.Errorf("%w: %s is too large to edit", ErrInvalid, rel)
			}
			continue
		}
		if utf8.Valid(data) {
			files = append(files, FileEntry{Path: rel, Content: string(data)})
		} else {
			files = append(files, FileEntry{Path: rel, Content: base64.StdEncoding.EncodeToString(data), IsBinary: true})
		}
	}
	return files, nil
}

// Update replaces a skill's files. Everything is validated first (frontmatter,
// paths), then the skill dir is removed and rewritten through the pinned,
// symlink-safe skills root, so a concurrent symlink swap can neither redirect
// the delete nor the writes out of the user's own tree. (A skill that was a
// symlink is replaced by a regular directory.)
func (s *Service) Update(source, name string, files []FileEntry) (CustomSkill, error) {
	sr, err := s.find(source, name)
	if err != nil {
		return CustomSkill{}, err
	}
	// Validate and decode everything before touching the real tree.
	type decoded struct {
		rel  string
		data []byte
	}
	var items []decoded
	var mdContent []byte
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f.Path))
		if f.Path == "" || filepath.IsAbs(f.Path) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, f.Path)
		}
		data := []byte(f.Content)
		if f.IsBinary {
			d, derr := base64.StdEncoding.DecodeString(f.Content)
			if derr != nil {
				return CustomSkill{}, fmt.Errorf("%w: Invalid base64 content for %s", ErrInvalid, f.Path)
			}
			data = d
		}
		items = append(items, decoded{rel: rel, data: data})
		if rel == skillMD {
			mdContent = data
		}
	}
	if mdContent == nil {
		return CustomSkill{}, fmt.Errorf("%w: %s is required", ErrInvalid, skillMD)
	}
	meta, err := ParseFrontmatter(string(mdContent))
	if err != nil {
		return CustomSkill{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}

	base, err := sr.openBase()
	if err != nil {
		return CustomSkill{}, err
	}
	defer base.Close()
	// Preserve the owner (the sandbox user for Docker) so the agent keeps edit
	// access; fall back to the skills dir's owner.
	uid, gid := skillOwner(base, name)
	if err := base.RemoveAll(name); err != nil {
		return CustomSkill{}, err
	}
	if err := base.Mkdir(name, 0o755); err != nil {
		return CustomSkill{}, err
	}
	for _, it := range items {
		full := path.Join(name, it.rel)
		if d := path.Dir(full); d != "." {
			if err := base.MkdirAll(d, 0o755); err != nil {
				return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, it.rel)
			}
		}
		if err := base.WriteFile(full, it.data, 0o644); err != nil {
			return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, it.rel)
		}
	}
	if os.Geteuid() == 0 && uid >= 0 {
		_ = fs.WalkDir(base.FS(), name, func(p string, d fs.DirEntry, err error) error {
			if err == nil {
				_ = base.Lchown(p, uid, gid)
			}
			return nil
		})
	}
	count, size := dirStats(base, name)
	return CustomSkill{Name: name, Description: description(meta), SizeBytes: size, FileCount: count, Sources: []string{source}}, nil
}

// skillOwner returns the uid/gid of the skill dir name under base, or -1,-1.
func skillOwner(base *os.Root, name string) (int, int) {
	fi, err := base.Stat(name)
	if err != nil {
		return -1, -1
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}
