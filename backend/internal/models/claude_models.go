package models

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

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

// ClaudeModels is the built-in model registry, ordered as in the model
// picker. Claude() extends it with what the CLI itself reports, so a new
// model shows up without a release; this list covers the time before any CLI
// has reported and keeps older models selectable.
var ClaudeModels = []ModelInfo{
	claudeModel("claude-opus-5-5", "Opus 5.5", false),
	claudeModel("claude-fable-5-2", "Fable 5.2", false),
	claudeModel("claude-sonnet-5-5", "Sonnet 5.5", false),
	claudeModel("claude-haiku-5-5", "Haiku 5.5", false),
	claudeModel("opus", "Opus (latest)", false),
	claudeModel("sonnet", "Sonnet (latest)", false),
	claudeModel("haiku", "Haiku (latest)", false),
	claudeModel("fable", "Fable (latest)", false),
	claudeModel("claude-opus-5", "Opus 5", true),
	claudeModel("claude-fable-5-1", "Fable 5.1", true),
	claudeModel("claude-fable-5", "Fable 5", true),
}

// LookupModel returns the registry entry for id.
func LookupModel(id string) (ModelInfo, bool) {
	for _, m := range Claude() {
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

// CLIModel is one entry of the model list in the Claude CLI's initialize
// reply: an alias (or "default") and the model it currently points to.
type CLIModel struct {
	Value         string   `json:"value"`
	ResolvedModel string   `json:"resolvedModel"`
	EffortLevels  []string `json:"supportedEffortLevels"`
}

// reported is the picker as last extended by a CLI report.
var reported struct {
	sync.RWMutex
	version string
	models  []ModelInfo
}

// Claude is the Claude model picker: the registry, extended by the newest
// CLI that reported its models.
func Claude() []ModelInfo {
	reported.RLock()
	defer reported.RUnlock()
	if reported.models != nil {
		return reported.models
	}
	return ClaudeModels
}

// ReportClaudeCLI folds a CLI's model list into the picker. Reports from a
// CLI older than the newest one seen are ignored. Each report extends the
// current picker rather than the bare registry, so a model only one account
// can see stays listed when another account's CLI reports.
func ReportClaudeCLI(version string, list []CLIModel) {
	reported.Lock()
	defer reported.Unlock()
	if reported.version != "" && CompareVersions(version, reported.version) < 0 {
		return
	}
	base := reported.models
	if base == nil {
		base = ClaudeModels
	}
	if merged := mergeCLIModels(base, list); merged != nil {
		reported.version, reported.models = version, merged
	}
}

var (
	pinnedIDRE = regexp.MustCompile(`^claude-[a-z]+(-[0-9]{1,2})+$`)
	aliasRE    = regexp.MustCompile(`^[a-z]+$`)
)

// mergeCLIModels lays out the picker as the registry does: the current
// model of each family, then the aliases, then legacy models. The model an
// alias resolves to is its family's current one, so a model the CLI has
// moved past becomes legacy. Returns nil when the report has nothing usable.
func mergeCLIModels(base []ModelInfo, list []CLIModel) []ModelInfo {
	known := map[string]ModelInfo{}
	for _, m := range base {
		known[m.ID] = m
	}
	current := map[string]string{} // family -> its current model id
	var pinned, aliases []ModelInfo
	seen := map[string]bool{}
	add := func(dst *[]ModelInfo, m ModelInfo) {
		if !seen[m.ID] {
			seen[m.ID] = true
			*dst = append(*dst, m)
		}
	}
	for _, c := range list {
		if c.Value == "default" || !pinnedIDRE.MatchString(c.ResolvedModel) {
			continue
		}
		m, ok := known[c.ResolvedModel]
		if !ok {
			m = claudeModel(c.ResolvedModel, displayName(c.ResolvedModel), false)
		}
		m.Legacy = false
		if len(c.EffortLevels) > 0 {
			m.EffortLevels = c.EffortLevels
		}
		current[family(m.ID)] = m.ID
		add(&pinned, m)
		if aliasRE.MatchString(c.Value) {
			a, ok := known[c.Value]
			if !ok {
				a = claudeModel(c.Value, strings.ToUpper(c.Value[:1])+c.Value[1:]+" (latest)", false)
			}
			a.EffortLevels = m.EffortLevels
			add(&aliases, a)
		}
	}
	if len(pinned) == 0 {
		return nil
	}
	// The rest of the base keeps its section; a model the CLI has moved its
	// family past joins the legacy ones, ahead of the older legacy models.
	var superseded, legacy []ModelInfo
	for _, m := range base {
		switch {
		case seen[m.ID]:
		case !strings.HasPrefix(m.ID, "claude-"):
			add(&aliases, m)
		case m.Legacy:
			legacy = append(legacy, m)
		case current[family(m.ID)] != "":
			m.Legacy = true
			superseded = append(superseded, m)
		default:
			add(&pinned, m)
		}
	}
	out := append(pinned, aliases...)
	out = append(out, superseded...)
	return append(out, legacy...)
}

// family is a model id's family: "fable" for claude-fable-5-2.
func family(id string) string {
	parts := strings.Split(id, "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// displayName names a model the registry doesn't know: claude-fable-5-2
// becomes "Fable 5.2".
func displayName(id string) string {
	parts := strings.Split(strings.TrimPrefix(id, "claude-"), "-")
	name := strings.ToUpper(parts[0][:1]) + parts[0][1:]
	if len(parts) > 1 {
		name += " " + strings.Join(parts[1:], ".")
	}
	return name
}

// CompareVersions orders dotted numeric versions ("2.1.321"); a part that
// isn't a number counts as 0.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
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
