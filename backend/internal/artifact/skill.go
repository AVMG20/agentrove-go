package artifact

import (
	_ "embed"
	"os"
	"path"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var skillTemplate string

// InstallSkill writes the built-in "artifacts" skill into a Claude config dir
// (the box's ~/claude), refreshing it whenever the app ships a new version.
//
// claudeDir is agent-writable and the agent is root in its box, so ~/claude
// could be replaced with a symlink out of the user's HOME. Writing through an
// os.Root anchored at HOME (the non-swappable mount parent of claudeDir) keeps
// the write inside the user's own tree even under a concurrent symlink swap.
func InstallSkill(claudeDir, frontendURL string) error {
	body := strings.ReplaceAll(skillTemplate, "{{KIT}}", strings.TrimRight(frontendURL, "/")+"/artifact-kit")
	root, err := os.OpenRoot(filepath.Dir(claudeDir))
	if err != nil {
		return err
	}
	defer root.Close()
	dir := path.Join(filepath.Base(claudeDir), "skills", "artifacts")
	p := path.Join(dir, "SKILL.md")
	if cur, err := root.ReadFile(p); err == nil && string(cur) == body {
		return nil
	}
	if err := root.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return root.WriteFile(p, []byte(body), 0o644)
}
