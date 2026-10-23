package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

// InstallSkill writes into ~/claude, which the agent (root in its box) can
// replace with a symlink out of HOME; the write must stay inside HOME.
func TestInstallSkillSymlinkSwapBlocked(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, "claude")
	// Normal case: writes the skill under the real ~/claude.
	if err := InstallSkill(claudeDir, "https://x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(claudeDir, "skills", "artifacts", "SKILL.md")); err != nil {
		t.Fatalf("skill not written: %v", err)
	}

	// Swap ~/claude for a symlink pointing outside HOME.
	outside := t.TempDir()
	if err := os.RemoveAll(claudeDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, claudeDir); err != nil {
		t.Fatal(err)
	}
	if err := InstallSkill(claudeDir, "https://x"); err == nil {
		t.Error("InstallSkill through a swapped ~/claude symlink must fail")
	}
	if _, err := os.Stat(filepath.Join(outside, "skills", "artifacts", "SKILL.md")); err == nil {
		t.Error("InstallSkill escaped HOME into the outside dir")
	}
}
