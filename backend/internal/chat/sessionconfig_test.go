package chat

import (
	"slices"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/config"
	"agentrove/internal/models"
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
