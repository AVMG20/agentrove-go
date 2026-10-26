package chat

import (
	"slices"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/config"
	"agentrove/internal/models"
	"agentrove/internal/profiles"
	"agentrove/internal/sandbox"
	appstore "agentrove/internal/store"
)

func TestSessionConfigBrowserTools(t *testing.T) {
	m, d := newTestModule(t)
	m.a = &app.App{Cfg: &config.Config{BrowserEnabled: true, BrowserIdleTTL: 10 * time.Minute}, DB: d, Store: &appstore.Store{DB: d}}
	agentrove := map[string]any{"agentrove": map[string]any{"type": "http"}}
	m.MCPConfig = func(string, string, string, bool) map[string]any { return agentrove }
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/p"}
	req := &turnRequest{chat: &models.Chat{ID: testChat}, userID: testUser}

	cfg := m.sessionConfig(sb, req)
	browser, ok := cfg.MCPServers["browser"].(map[string]any)
	if !ok || browser["command"] != "agentrove-browser-mcp" {
		t.Fatalf("browser server = %v", cfg.MCPServers["browser"])
	}
	if browser["env"].(map[string]string)["AGENTROVE_BROWSER_IDLE_SECONDS"] != "600" {
		t.Fatalf("env = %v", browser["env"])
	}
	if _, ok := agentrove["browser"]; ok {
		t.Fatal("mutated the shared MCPConfig map")
	}
	allow := cfg.Settings["permissions"].(map[string]any)["allow"].([]string)
	if !slices.Contains(allow, "mcp__browser__browser_click") || !slices.Contains(allow, "mcp__agentrove__create_artifact") {
		t.Fatalf("allow = %v", allow)
	}
	if slices.Contains(allow, "mcp__browser__browser_run_code_unsafe") {
		t.Fatal("arbitrary-code tool pre-allowed")
	}
	if !strings.Contains(cfg.SystemPrompt, "<browser>") {
		t.Fatalf("prompt = %q", cfg.SystemPrompt)
	}

	// Turned off in the user's settings: no server, no prompt.
	if _, err := d.Exec(`UPDATE user_settings SET browser_tools_enabled = 0 WHERE user_id = ?`, testUser); err != nil {
		t.Fatal(err)
	}
	cfg = m.sessionConfig(sb, req)
	if _, ok := cfg.MCPServers["browser"]; ok || strings.Contains(cfg.SystemPrompt, "<browser>") {
		t.Fatalf("browser tools still configured: %v", cfg.MCPServers)
	}
	if allow := cfg.Settings["permissions"].(map[string]any)["allow"].([]string); slices.Contains(allow, "mcp__browser__browser_click") {
		t.Fatalf("allow = %v", allow)
	}
}

// A Claude profile keeps the user's own model aliases and drops
// only the deployment's headers and aliases.
func TestSessionConfigKeepsUserAliasesOnProfiles(t *testing.T) {
	m, d := newTestModule(t)
	m.a = &app.App{Cfg: &config.Config{}, DB: d, Store: &appstore.Store{DB: d}}
	if _, err := m.a.Store.GetUserSettings(t.Context(), testUser); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`UPDATE user_settings SET custom_env_vars = ? WHERE user_id = ?`,
		`[{"key":"ANTHROPIC_DEFAULT_SONNET_MODEL","value":"my-sonnet"},{"key":"ANTHROPIC_API_KEY","value":"server"}]`, testUser); err != nil {
		t.Fatal(err)
	}
	sb := &sandbox.Sandbox{Kind: sandbox.KindDocker, Root: "/home/user/projects/p"}
	p := &profiles.Profile{ID: "p", Provider: models.ProviderClaude, AuthMode: profiles.AuthToken, Token: "sk-ant-oat01-x"}
	req := &turnRequest{chat: &models.Chat{ID: testChat}, userID: testUser, profile: p}

	cfg := m.sessionConfig(sb, req)
	if cfg.Env["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "my-sonnet" || cfg.Env["ANTHROPIC_API_KEY"] != "" {
		t.Fatalf("env = %v", cfg.Env)
	}
	if !slices.Contains(cfg.UnsetGlobal, "ANTHROPIC_CUSTOM_HEADERS") || slices.Contains(cfg.Unset, "ANTHROPIC_DEFAULT_SONNET_MODEL") {
		t.Fatalf("unset %v, unset global %v", cfg.Unset, cfg.UnsetGlobal)
	}
}
