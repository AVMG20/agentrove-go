package profiles

import (
	"context"
	"testing"

	"agentrove/internal/models"
)

func TestParseAgyModels(t *testing.T) {
	out := "Fetching available models...\n" +
		"gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)\n" +
		"claude-sonnet-4-6\tClaude Sonnet 4.6 (Thinking)\n" +
		"Error: something\n\n"
	got := ParseAgyModels(out)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].ID != "gemini-4-hydrogen-high" || got[0].Name != "Gemini 4 Hydrogen (High)" ||
		got[0].AgentKind != models.ProviderAntigravity || got[0].ContextWindow != 1_000_000 {
		t.Fatalf("new gemini model = %+v", got[0])
	}
	if got[1].ContextWindow != 200_000 {
		t.Fatalf("known model keeps its window: %+v", got[1])
	}
	if len(ParseAgyModels("Error: Please sign in to view available models.\n")) != 0 {
		t.Fatal("parsed a sign-in error as models")
	}
}

// A saved list survives a restart and is dropped when the account changes.
func TestAgyModelsPersist(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p := &Profile{ID: "p1", UserID: testUser, Provider: models.ProviderAntigravity, AuthMode: AuthToken}
	if _, ok := s.agyModels(ctx, p); ok {
		t.Fatal("list without a box or a saved one")
	}
	want := ParseAgyModels("gemini-4-hydrogen-high\tGemini 4 Hydrogen (High)\n")
	s.saveAgyModels(p, want)
	s.agyModelCache = &agyModelCache{entries: map[string]agyModelEntry{}} // restart
	got, ok := s.agyModels(ctx, p)
	if !ok || len(got) != 1 || got[0].ID != want[0].ID {
		t.Fatalf("after restart: %+v, %v", got, ok)
	}
	s.forgetAgyModels(p)
	if _, ok := s.agyModels(ctx, p); ok {
		t.Fatal("forgotten list still served")
	}
}
