package profiles

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The agentrove server is merged into agy's MCP config next to the user's
// own servers, updated when its token changes and removed with the rules
// when a turn has none.
func TestAgyChatScriptMergesMCPServer(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	mcpFile, rulesFile := filepath.Join(dir, "config", "mcp_config.json"), filepath.Join(dir, "config", "rules", "agentrove.md")
	if err := os.MkdirAll(filepath.Dir(mcpFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mcpFile, []byte(`{"mcpServers":{"mine":{"serverUrl":"https://mine"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(server, rules string) {
		t.Helper()
		cmd := exec.Command("bash", "-c", agyChatScript(mcpFile, rulesFile, rules))
		cmd.Env = os.Environ()
		if server != "" {
			cmd.Env = append(cmd.Env, AgentroveMCPEnv+"="+server)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("chat script: %v %s", err, out)
		}
	}
	servers := func() map[string]any {
		s, _ := readAgySettings(t, mcpFile)["mcpServers"].(map[string]any)
		return s
	}

	run(`{"url":"https://api/mcp","headers":{"Authorization":"Bearer t1"}}`, "Use artifacts.")
	got := servers()
	ar, _ := got["agentrove"].(map[string]any)
	if got["mine"] == nil || ar["serverUrl"] != "https://api/mcp" || ar["headers"].(map[string]any)["Authorization"] != "Bearer t1" {
		t.Fatalf("servers = %v", got)
	}
	if b, err := os.ReadFile(rulesFile); err != nil || !strings.Contains(string(b), "trigger: always_on") || !strings.Contains(string(b), "Use artifacts.") {
		t.Fatalf("rules = %q, %v", b, err)
	}

	run(`{"url":"https://api/mcp","headers":{"Authorization":"Bearer t2"}}`, "Use artifacts.")
	if h := servers()["agentrove"].(map[string]any)["headers"].(map[string]any); h["Authorization"] != "Bearer t2" {
		t.Fatalf("token not updated: %v", h)
	}

	run("", "")
	if got := servers(); got["agentrove"] != nil || got["mine"] == nil {
		t.Fatalf("servers after removal = %v", got)
	}
	if _, err := os.Stat(rulesFile); !os.IsNotExist(err) {
		t.Fatalf("rules file kept: %v", err)
	}
}
