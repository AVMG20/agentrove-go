package models

import "regexp"

// Agent providers a profile (and so a chat) runs on.
const (
	ProviderClaude      = "claude"
	ProviderCodex       = "codex"
	ProviderAntigravity = "antigravity"
)

var Providers = []string{ProviderClaude, ProviderCodex, ProviderAntigravity}

func ValidProvider(p string) bool {
	for _, v := range Providers {
		if v == p {
			return true
		}
	}
	return false
}

// CodexEffortLevels are what codex's model_reasoning_effort accepts (minimal
// is left out: it disables tools on some models).
var CodexEffortLevels = []string{"low", "medium", "high", "xhigh"}

func codexModel(id, name string, window int) ModelInfo {
	return ModelInfo{ID: id, Name: name, AgentKind: ProviderCodex, ContextWindow: window, EffortLevels: CodexEffortLevels}
}

// CodexModels are the models a ChatGPT-plan Codex login lists (`codex debug
// models`); API keys may reach more, so other gpt-* ids are accepted too.
var CodexModels = []ModelInfo{
	codexModel("gpt-6-luna", "GPT 6 Luna", 272_000),
	codexModel("gpt-5.6-terra", "GPT 5.6 Terra", 272_000),
	codexModel("gpt-5.6-luna", "GPT 5.6 Luna", 272_000),
}

// AntigravityModels are what `agy models` lists. The reasoning level is part
// of the id, so they expose no separate effort dial.
var AntigravityModels = []ModelInfo{
	{ID: "gemini-3.8-flash-high", Name: "Gemini 3.8 Flash (High)", AgentKind: ProviderAntigravity, ContextWindow: 1_000_000},
	{ID: "gemini-3.8-flash-medium", Name: "Gemini 3.8 Flash (Medium)", AgentKind: ProviderAntigravity, ContextWindow: 1_000_000},
	{ID: "gemini-3.8-flash-low", Name: "Gemini 3.8 Flash (Low)", AgentKind: ProviderAntigravity, ContextWindow: 1_000_000},
	{ID: "claude-opus-4-6-thinking", Name: "Claude Opus 4.6 (Thinking)", AgentKind: ProviderAntigravity, ContextWindow: 200_000},
	{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6 (Thinking)", AgentKind: ProviderAntigravity, ContextWindow: 200_000},
	{ID: "gpt-oss-120b-medium", Name: "GPT-OSS 120B (Medium)", AgentKind: ProviderAntigravity, ContextWindow: 128_000},
}

// ModelsFor lists the selectable models of a provider.
func ModelsFor(provider string) []ModelInfo {
	switch provider {
	case ProviderCodex:
		return CodexModels
	case ProviderAntigravity:
		return AntigravityModels
	case ProviderClaude:
		return ClaudeModels
	}
	return nil
}

// AllModels is every provider's models, Claude first.
func AllModels() []ModelInfo {
	out := make([]ModelInfo, 0, len(ClaudeModels)+len(CodexModels)+len(AntigravityModels))
	for _, p := range Providers {
		out = append(out, ModelsFor(p)...)
	}
	return out
}

// ProviderOf is the provider whose registry lists id ("" for none).
func ProviderOf(id string) string {
	for _, p := range Providers {
		if _, ok := LookupProviderModel(p, id); ok {
			return p
		}
	}
	return ""
}

// LookupProviderModel returns a provider's registry entry for id.
func LookupProviderModel(provider, id string) (ModelInfo, bool) {
	for _, m := range ModelsFor(provider) {
		if m.ID == id {
			return m, true
		}
	}
	return ModelInfo{}, false
}

var (
	codexModelRE       = regexp.MustCompile(`^(gpt|o[0-9]|codex)[a-z0-9._-]{0,62}$`)
	antigravityModelRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,79}$`)
)

// ValidModelFor accepts a provider's registry ids plus ids shaped like that
// provider's, so new models work before the registry knows them.
func ValidModelFor(provider, id string) bool {
	if _, ok := LookupProviderModel(provider, id); ok {
		return true
	}
	switch provider {
	case ProviderClaude:
		return ValidModelID(id)
	case ProviderCodex:
		return codexModelRE.MatchString(id)
	case ProviderAntigravity:
		return antigravityModelRE.MatchString(id)
	}
	return false
}

// DefaultModel is the model a provider's turns fall back to when the
// requested one belongs to another provider (automations, MCP).
func DefaultModel(provider string) string {
	if ms := ModelsFor(provider); len(ms) > 0 {
		return ms[0].ID
	}
	return ""
}

// ModelContextWindow is the context size of a provider's model; unknown ids
// get a conservative default.
func ModelContextWindow(provider, id string) int {
	if m, ok := LookupProviderModel(provider, id); ok {
		return m.ContextWindow
	}
	if provider == ProviderCodex {
		return 272_000
	}
	return 200_000
}
