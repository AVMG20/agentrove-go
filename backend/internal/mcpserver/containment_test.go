package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentrove/internal/automation"
	"agentrove/internal/db"
	"agentrove/internal/models"
)

const wsID = "22222222-2222-2222-2222-222222222222"

// toolErr calls a tool that must fail and returns its message.
func toolErr(t *testing.T, m *Module, c *caller, name string, args any) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	res := rpc(t, m, c, string(b))["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("%s succeeded, want error: %v", name, res)
	}
	return res["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestChatComesFromTokenNotHeader(t *testing.T) {
	m := newModule(t)
	cfg, err := ServerConfigForChat(m.a, userID, "chat-a", true)
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(cfg["headers"].(map[string]any)["Authorization"].(string), "Bearer ")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set(ChatIDHeader, "chat-b") // forged by the box
	c := m.authenticate(req)
	if c == nil || c.userID != userID || !c.fullAccess || c.chatID != "chat-a" {
		t.Fatalf("caller = %+v, want chat-a from the token", c)
	}
}

func TestAgentCannotRewriteUserPersona(t *testing.T) {
	m := newModule(t)
	user := []models.Persona{{Name: "Mine", Content: "user prompt"}}
	if _, err := m.a.Store.GetUserSettings(t.Context(), userID); err != nil {
		t.Fatal(err)
	}
	if err := m.savePersonas(t.Context(), userID, user); err != nil {
		t.Fatal(err)
	}
	full := &caller{userID: userID, fullAccess: true}
	toolErr(t, m, full, "update_persona", map[string]any{"name": "Mine", "content": "injected"})
	toolErr(t, m, full, "delete_persona", map[string]any{"name": "Mine"})
	if p := callTool(t, m, full, "get_persona", map[string]any{"name": "Mine"})["persona"].(map[string]any); p["content"] != "user prompt" {
		t.Fatalf("user persona changed: %v", p)
	}
}

func TestAgentAutomationBounds(t *testing.T) {
	m := newModule(t)
	now := db.Now()
	if _, err := m.a.DB.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path, created_at, updated_at)
		VALUES (?, 'ws', ?, 'sb', 'host', '/tmp', ?, ?)`, wsID, userID, now, now); err != nil {
		t.Fatal(err)
	}
	ws, name, prompt, model, cron, plan := wsID, "nightly", "user's prompt", models.ClaudeModels[0].ID, "0 3 * * *", "plan"
	mine, err := m.automations.Create(t.Context(), userID, &automation.Input{WorkspaceID: &ws, Name: &name, Prompt: &prompt,
		ModelID: &model, CronExpression: &cron, PermissionMode: &plan, Set: map[string]bool{}}, "")
	if err != nil {
		t.Fatal(err)
	}
	full := &caller{userID: userID, fullAccess: true}
	toolErr(t, m, full, "update_automation", map[string]any{"automation_id": mine.ID, "prompt": "injected"})
	toolErr(t, m, full, "delete_automation", map[string]any{"automation_id": mine.ID})

	created := callTool(t, m, full, "create_automation", map[string]any{"name": "a", "prompt": "p", "cron_expression": cron})["automation"].(map[string]any)
	if created["created_by_agent"] != true {
		t.Fatalf("not marked: %v", created)
	}
	id := created["id"].(string)
	callTool(t, m, full, "update_automation", map[string]any{"automation_id": id, "prompt": "p2"})

	// A model switch must not raise the permission mode.
	if _, err := m.a.DB.Exec(`UPDATE automations SET permission_mode = 'plan' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	callTool(t, m, full, "update_automation", map[string]any{"automation_id": id, "model_id": model})
	if x, _ := m.automations.Get(t.Context(), id, userID); x.PermissionMode != "plan" {
		t.Fatalf("permission mode = %q, want plan", x.PermissionMode)
	}
	callTool(t, m, full, "delete_automation", map[string]any{"automation_id": id})

	for i := range maxAgentAutomations {
		callTool(t, m, full, "create_automation", map[string]any{"name": "n" + string(rune('a'+i)), "prompt": "p", "cron_expression": cron})
	}
	toolErr(t, m, full, "create_automation", map[string]any{"name": "over", "prompt": "p", "cron_expression": cron})
}
