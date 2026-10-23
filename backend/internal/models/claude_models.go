package models

import "strings"

// ModelInfo describes one selectable Claude model. Ids are what the CLI's
// --model accepts; aliases resolve inside the CLI.
type ModelInfo struct {
	ID            string   `json:"model_id"`
	Name          string   `json:"name"`
	AgentKind     string   `json:"agent_kind"`
	ContextWindow int      `json:"context_window"`
	Legacy        bool     `json:"legacy"`
	EffortLevels  []string `json:"thinking_modes"`
}

// EffortLevels are the reasoning-effort tiers, lowest to highest.
var EffortLevels = []string{"low", "medium", "high", "xhigh", "max"}

func claudeModel(id, name string, legacy bool) ModelInfo {
	return ModelInfo{ID: id, Name: name, AgentKind: "claude", ContextWindow: 1_000_000, Legacy: legacy, EffortLevels: EffortLevels}
}

// ClaudeModels is the single model registry, ordered as in the model picker.
var ClaudeModels = []ModelInfo{
	claudeModel("claude-opus-5-5", "Opus 5.5", false),
	claudeModel("claude-fable-5-1", "Fable 5.1", false),
	claudeModel("claude-sonnet-5-5", "Sonnet 5.5", false),
	claudeModel("claude-haiku-5-5", "Haiku 5.5", false),
	claudeModel("opus", "Opus (latest)", false),
	claudeModel("sonnet", "Sonnet (latest)", false),
	claudeModel("haiku", "Haiku (latest)", false),
	claudeModel("fable", "Fable (latest)", false),
	claudeModel("claude-opus-5", "Opus 5", true),
	claudeModel("claude-fable-5", "Fable 5", true),
}

// LookupModel returns the registry entry for id.
func LookupModel(id string) (ModelInfo, bool) {
	for _, m := range ClaudeModels {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

// ValidModelID accepts registry ids plus any other claude-* id, so new
// models work before the registry is updated.
func ValidModelID(id string) bool {
	if _, ok := LookupModel(id); ok {
		return true
	}
	return len(id) > 7 && len(id) <= 128 && strings.HasPrefix(id, "claude-")
}

// Claude permission modes. DefaultPermissionMode asks before acting;
// FullAccessPermissionMode is what unattended runs use.
const (
	DefaultPermissionMode    = "default"
	FullAccessPermissionMode = "bypassPermissions"
	DefaultPersonaName       = "Default"
)

var PermissionModes = []string{"default", "acceptEdits", "plan", "bypassPermissions", "auto"}

func ValidPermissionMode(mode string) bool {
	for _, m := range PermissionModes {
		if m == mode {
			return true
		}
	}
	return false
}
