package artifact

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skill/SKILL.md
var skillTemplate string

// InstallSkill writes the built-in "artifacts" skill into a Claude config dir
// (the box's ~/claude), refreshing it whenever the app ships a new version.
func InstallSkill(claudeDir, frontendURL string) error {
	body := strings.ReplaceAll(skillTemplate, "{{KIT}}", strings.TrimRight(frontendURL, "/")+"/artifact-kit")
	p := filepath.Join(claudeDir, "skills", "artifacts", "SKILL.md")
	if cur, err := os.ReadFile(p); err == nil && string(cur) == body {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(body), 0o644)
}
