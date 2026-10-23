// Package skills lists and edits Claude skills (<dir>/<name>/SKILL.md) from
// the workspace's .claude/skills, the user's Claude config dir and enabled
// Claude plugins, and serves /api/v1/skills.
package skills

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
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

// skillRoot is a directory of skills plus the tree it must stay inside. The
// workspace and the Claude config dir are written by the agent, so a symlink
// there must not lead the API (which runs outside the box) to other host files.
type skillRoot struct{ base, root string }

type Service struct {
	paths []skillRoot // search order: workspace first, then user, then plugins
}

// maxSkillFile bounds one file the API reads from an agent-writable skill
// dir; it matches the size Update accepts.
const maxSkillFile = 10_000_000

// strictlyWithin reports whether p, with symlinks resolved, is below root.
func strictlyWithin(root, p string) bool {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	realP, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(realRoot, realP)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// readSkillFile reads dir/rel without following symlinks out of dir.
func readSkillFile(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
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
		{filepath.Join(ws.WorkspacePath, ".claude", "skills"), ws.WorkspacePath},
		{filepath.Join(claudeDir, "skills"), claudeDir},
	}
	containerClaude := ""
	if ws.SandboxProvider == sandbox.KindDocker {
		containerClaude = sandbox.ContainerClaudeDir
	}
	paths = append(paths, pluginSkillPaths(claudeDir, containerClaude)...)
	return &Service{paths: paths}
}

// pluginSkillPaths cross-references settings.json (enabledPlugins) with
// plugins/installed_plugins.json (install paths). Install paths recorded
// inside a Docker sandbox are mapped back onto the host config dir.
func pluginSkillPaths(claudeDir, containerClaude string) []skillRoot {
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
			dir := filepath.Join(p, "skills")
			// installed_plugins.json is agent-writable: in a Docker sandbox the
			// install path must resolve inside the config dir, not to any
			// host directory (another user's workspace) named "skills".
			root := p
			if containerClaude != "" {
				root = claudeDir
			}
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() && strictlyWithin(root, dir) {
				out = append(out, skillRoot{dir, root})
			}
		}
	}
	return out
}

func readJSON(p string, v any) bool {
	b, err := os.ReadFile(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func dirStats(dir string) (count int, size int64) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, err := os.Stat(p)
		if err == nil && fi.Mode().IsRegular() {
			count++
			size += fi.Size()
		}
		return nil
	})
	return count, size
}

func description(meta map[string]string) string { return meta["description"] }

// List returns every skill, deduplicated by resolved directory.
func (s *Service) List() []CustomSkill {
	out := []CustomSkill{}
	seenDirs := map[string]bool{}
	seenNames := map[string]bool{}
	for _, sr := range s.paths {
		base := sr.base
		if !strictlyWithin(sr.root, base) {
			continue
		}
		entries, err := os.ReadDir(base)
		if err != nil {
			continue
		}
		for _, e := range entries {
			dir := filepath.Join(base, e.Name())
			fi, err := os.Stat(dir)
			if err != nil || !fi.IsDir() || seenNames[e.Name()] || !strictlyWithin(base, dir) {
				continue
			}
			md := filepath.Join(dir, skillMD)
			if _, err := os.Stat(md); err != nil {
				continue
			}
			resolved, err := filepath.EvalSymlinks(dir)
			if err != nil {
				resolved = dir
			}
			if seenDirs[resolved] {
				seenNames[e.Name()] = true
				continue
			}
			content, err := readSkillFile(dir, skillMD)
			if err != nil || !utf8.Valid(content) {
				continue
			}
			meta, err := ParseFrontmatter(string(content))
			if err != nil {
				meta = map[string]string{}
			}
			count, size := dirStats(dir)
			seenDirs[resolved] = true
			seenNames[e.Name()] = true
			out = append(out, CustomSkill{Name: e.Name(), Description: description(meta), SizeBytes: size, FileCount: count, Sources: []string{Source}})
		}
	}
	return out
}

func validName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}

func (s *Service) find(source, name string) (string, error) {
	if source != Source || !validName(name) {
		return "", fmt.Errorf("%w: Skill '%s' not found", ErrNotFound, name)
	}
	for _, sr := range s.paths {
		dir := filepath.Join(sr.base, name)
		if !strictlyWithin(sr.root, sr.base) || !strictlyWithin(sr.base, dir) {
			continue
		}
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			if _, err := readSkillFile(dir, skillMD); err == nil {
				return dir, nil
			}
		}
	}
	return "", fmt.Errorf("%w: Skill '%s' not found", ErrNotFound, name)
}

// Files returns every file of a skill; non-UTF-8 files come back base64.
func (s *Service) Files(source, name string) ([]FileEntry, error) {
	dir, err := s.find(source, name)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var paths []string
	_ = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// Stat follows symlinks, but only to targets inside the skill dir.
		if fi, err := root.Stat(p); err == nil && fi.Mode().IsRegular() {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	files := []FileEntry{}
	for _, rel := range paths {
		data, err := readSkillFile(dir, rel)
		if err != nil {
			if fi, serr := root.Stat(rel); serr == nil && fi.Size() > maxSkillFile {
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

// Update replaces a skill's files atomically-ish: everything is written and
// SKILL.md validated in a temp dir first, then swapped in. Symlinked skills
// are replaced at their target so the link keeps pointing at it.
func (s *Service) Update(source, name string, files []FileEntry) (CustomSkill, error) {
	dir, err := s.find(source, name)
	if err != nil {
		return CustomSkill{}, err
	}
	tmpRoot, err := os.MkdirTemp("", "agentrove-skill-")
	if err != nil {
		return CustomSkill{}, err
	}
	defer os.RemoveAll(tmpRoot)
	tmp := filepath.Join(tmpRoot, name)
	if err := os.Mkdir(tmp, 0o755); err != nil {
		return CustomSkill{}, err
	}
	root, err := os.OpenRoot(tmp)
	if err != nil {
		return CustomSkill{}, err
	}
	defer root.Close()
	for _, f := range files {
		rel := filepath.ToSlash(filepath.Clean(f.Path))
		if f.Path == "" || filepath.IsAbs(f.Path) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, f.Path)
		}
		data := []byte(f.Content)
		if f.IsBinary {
			if data, err = base64.StdEncoding.DecodeString(f.Content); err != nil {
				return CustomSkill{}, fmt.Errorf("%w: Invalid base64 content for %s", ErrInvalid, f.Path)
			}
		}
		if d := filepath.Dir(rel); d != "." {
			if err := root.MkdirAll(d, 0o755); err != nil {
				return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, f.Path)
			}
		}
		if err := root.WriteFile(rel, data, 0o644); err != nil {
			return CustomSkill{}, fmt.Errorf("%w: Invalid file path: %s", ErrInvalid, f.Path)
		}
	}
	md, err := os.ReadFile(filepath.Join(tmp, skillMD))
	if err != nil {
		return CustomSkill{}, fmt.Errorf("%w: %s is required", ErrInvalid, skillMD)
	}
	meta, err := ParseFrontmatter(string(md))
	if err != nil {
		return CustomSkill{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	count, size := dirStats(tmp)

	target, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return CustomSkill{}, err
	}
	uid, gid := -1, -1
	if fi, err := os.Stat(target); err == nil {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
	}
	if err := os.RemoveAll(target); err != nil {
		return CustomSkill{}, err
	}
	if err := copyTree(tmp, target); err != nil {
		return CustomSkill{}, err
	}
	// Keep the skill owned by whoever owned it (the sandbox user for Docker).
	if os.Geteuid() == 0 && uid >= 0 {
		_ = filepath.WalkDir(target, func(p string, d fs.DirEntry, err error) error {
			if err == nil {
				_ = os.Lchown(p, uid, gid)
			}
			return nil
		})
	}
	return CustomSkill{Name: name, Description: description(meta), SizeBytes: size, FileCount: count, Sources: []string{source}}, nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		f, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, in); err != nil {
			f.Close()
			return err
		}
		return f.Close()
	})
}
