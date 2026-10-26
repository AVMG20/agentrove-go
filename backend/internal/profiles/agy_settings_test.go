package profiles

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"agentrove/internal/models"
)

func runAgySettings(t *testing.T, file, provider string) {
	t.Helper()
	out, err := exec.Command("bash", "-c", agySettingsScript(file, provider)).CombinedOutput()
	if err != nil {
		t.Fatalf("settings script: %v %s", err, out)
	}
}

func readAgySettings(t *testing.T, file string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// agy ignores GEMINI_API_KEY unless its settings name the Gemini API as the
// model provider; a Google sign-in profile must not keep that setting.
func TestAgySettingsFollowAuthMode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	file := filepath.Join(t.TempDir(), ".gemini/antigravity-cli/settings.json")

	// Google sign-in on a fresh HOME writes nothing.
	runAgySettings(t, file, "")
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("sign-in mode created settings: %v", err)
	}
	// API key: created.
	runAgySettings(t, file, agyKeyProvider)
	if s := readAgySettings(t, file); s["modelProvider"] != "gemini" {
		t.Fatalf("settings = %v", s)
	}
	// Merged into the CLI's own settings, which are kept.
	if err := os.WriteFile(file, []byte(`{"theme":"dark","modelProvider":"google"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runAgySettings(t, file, agyKeyProvider)
	if s := readAgySettings(t, file); s["modelProvider"] != "gemini" || s["theme"] != "dark" {
		t.Fatalf("merged settings = %v", s)
	}
	// Back to Google sign-in: the key's provider goes, the rest stays.
	runAgySettings(t, file, "")
	if s := readAgySettings(t, file); s["modelProvider"] != nil || s["theme"] != "dark" {
		t.Fatalf("after switching back = %v", s)
	}
}

func TestAgyLaunchSetsUpTheProvider(t *testing.T) {
	key := &Profile{ID: "p", Provider: models.ProviderAntigravity, AuthMode: AuthToken, Token: "g-key"}
	l := key.Launch()
	if l.Env["GEMINI_API_KEY"] != "g-key" || !strings.Contains(l.Setup, "'/home/user/.agentrove/profiles/p/home/.gemini/antigravity-cli/settings.json' 'gemini'") {
		t.Fatalf("api key launch = %+v", l)
	}
	login := &Profile{ID: "p", Provider: models.ProviderAntigravity, AuthMode: AuthLogin}
	if l := login.Launch(); !strings.HasSuffix(l.Setup, "settings.json' ''") {
		t.Fatalf("sign-in launch = %s", l.Setup)
	}
}

// Gateway headers and model aliases from the deployment don't follow a
// Claude profile to its own endpoint, but the user's own ones do (a migrated
// gateway profile keeps the aliases its env vars set).
func TestClaudeProfilesDropDeploymentHeadersAndAliases(t *testing.T) {
	p := &Profile{ID: "p", Provider: models.ProviderClaude, AuthMode: AuthToken, Token: "sk-ant-oat01-x"}
	global := map[string]string{"ANTHROPIC_CUSTOM_HEADERS": "X-Gw: 1", "ANTHROPIC_MODEL": "gw-opus",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "gw-sonnet", "FOO": "bar"}
	env := p.ApplyEnv(global, map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "my-sonnet"})
	for _, k := range []string{"ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL"} {
		if _, ok := env[k]; ok {
			t.Errorf("deployment %s reaches the profile", k)
		}
	}
	if env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "my-sonnet" || env["FOO"] != "bar" {
		t.Errorf("user and unrelated vars must stay: %v", env)
	}
	l := p.Launch()
	for _, k := range []string{"ANTHROPIC_CUSTOM_HEADERS", "ANTHROPIC_MODEL", "ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL"} {
		if !slices.Contains(l.UnsetGlobal, k) {
			t.Errorf("%s not dropped from the deployment env", k)
		}
		if slices.Contains(l.Unset, k) {
			t.Errorf("%s dropped from the user's env vars", k)
		}
	}
}
