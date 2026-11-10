package models

import (
	"reflect"
	"testing"
)

func resetReported(t *testing.T) {
	t.Helper()
	reset := func() {
		reported.Lock()
		reported.version, reported.models = "", nil
		reported.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func ids(ms []ModelInfo) (out []string) {
	for _, m := range ms {
		id := m.ID
		if m.Legacy {
			id += " (legacy)"
		}
		out = append(out, id)
	}
	return out
}

func cliList(fable string) []CLIModel {
	return []CLIModel{
		{Value: "default", ResolvedModel: "claude-opus-5-5"},
		{Value: "opus", ResolvedModel: "claude-opus-5-5"},
		{Value: "fable", ResolvedModel: fable, EffortLevels: []string{"low", "high"}},
		{Value: "sonnet", ResolvedModel: "claude-sonnet-5-5"},
		{Value: "haiku", ResolvedModel: "claude-haiku-5-5"},
	}
}

// Without a report the picker is the registry.
func TestClaudeWithoutReport(t *testing.T) {
	resetReported(t)
	if !reflect.DeepEqual(Claude(), ClaudeModels) {
		t.Fatal("picker differs from the registry")
	}
}

// A new model the CLI points an alias at joins the current models, and the
// one it replaced moves to legacy.
func TestReportClaudeCLINewModel(t *testing.T) {
	resetReported(t)
	ReportClaudeCLI("2.1.400", cliList("claude-fable-5-3"))
	want := []string{
		"claude-opus-5-5", "claude-fable-5-3", "claude-sonnet-5-5", "claude-haiku-5-5",
		"opus", "fable", "sonnet", "haiku",
		"claude-fable-5-2 (legacy)", "claude-opus-5 (legacy)", "claude-fable-5-1 (legacy)", "claude-fable-5 (legacy)",
	}
	if got := ids(Claude()); !reflect.DeepEqual(got, want) {
		t.Fatalf("picker = %v\nwant %v", got, want)
	}
	m, ok := LookupModel("claude-fable-5-3")
	if !ok || m.Name != "Fable 5.3" || m.AgentKind != ProviderClaude || !reflect.DeepEqual(m.EffortLevels, []string{"low", "high"}) {
		t.Fatalf("fable 5.3 = %+v", m)
	}
	if ProviderOf("claude-fable-5-3") != ProviderClaude || DefaultModel(ProviderClaude) != "claude-opus-5-5" {
		t.Fatal("new model not resolvable")
	}
}

// An older CLI can't roll the picker back; a report missing a family keeps
// that family's models.
func TestReportClaudeCLIOrdering(t *testing.T) {
	resetReported(t)
	ReportClaudeCLI("2.1.400", cliList("claude-fable-5-3"))
	ReportClaudeCLI("2.1.399", cliList("claude-fable-5-2"))
	if m, _ := LookupModel("claude-fable-5-2"); !m.Legacy {
		t.Fatal("older CLI report applied")
	}
	ReportClaudeCLI("2.1.400", []CLIModel{{Value: "opus", ResolvedModel: "claude-opus-5-5"}})
	if m, ok := LookupModel("claude-fable-5-3"); !ok || m.Legacy {
		t.Fatalf("fable 5.3 = %+v, %v", m, ok)
	}
}

// Reports without usable ids leave the registry alone.
func TestReportClaudeCLIIgnoresForeignIDs(t *testing.T) {
	resetReported(t)
	ReportClaudeCLI("2.1.400", []CLIModel{{Value: "opus", ResolvedModel: "us.anthropic.claude-opus-5-5-v1:0"}, {Value: "default", ResolvedModel: "claude-opus-5-5"}})
	if !reflect.DeepEqual(Claude(), ClaudeModels) {
		t.Fatalf("picker = %v", ids(Claude()))
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"2.1.321", "2.1.319", 1}, {"2.1.9", "2.1.10", -1}, {"2.1", "2.1.0", 0}, {"3.0.0", "2.99.99", 1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%s, %s) = %d", c.a, c.b, got)
		}
	}
}

func TestDisplayName(t *testing.T) {
	if got := displayName("claude-fable-5-2"); got != "Fable 5.2" {
		t.Fatal(got)
	}
	if got := displayName("claude-opus-6"); got != "Opus 6" {
		t.Fatal(got)
	}
}
