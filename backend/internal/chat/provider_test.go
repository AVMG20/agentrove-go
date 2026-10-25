package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/events"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
	"agentrove/internal/profiles"
)

func withProfiles(t *testing.T, m *Module) *profiles.Service {
	t.Helper()
	keys, err := auth.NewKeys(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	m.a.Profiles = profiles.NewService(m.a.DB, keys, &config.Config{StoragePath: t.TempDir()}, nil)
	m.a.Hub = events.NewHub()
	return m.a.Profiles
}

func patchChat(m *Module, chatID, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPatch, "/api/v1/chat/chats/"+chatID, strings.NewReader(body))
	r.SetPathValue("chat_id", chatID)
	r = r.WithContext(auth.WithUser(r.Context(), &models.User{ID: testUser}))
	w := httptest.NewRecorder()
	httpx.Wrap(m.updateChat)(w, r)
	return w
}

func TestNewChatsRunOnTheRequestedOrDefaultProfile(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	ps := withProfiles(t, m)
	ctx := context.Background()
	codexName, codexProvider := "Codex", models.ProviderCodex
	cx, err := ps.Create(ctx, testUser, profiles.Input{Provider: &codexProvider, Name: &codexName})
	if err != nil {
		t.Fatal(err)
	}

	c, err := m.CreateChat(ctx, testUser, "t", testWS, nil, cx.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ProfileID != cx.ID || c.Provider != models.ProviderCodex {
		t.Fatalf("chat profile = %s/%s", c.ProfileID, c.Provider)
	}

	// No profile: the default (built-in until set).
	c, err = m.CreateChat(ctx, testUser, "t", testWS, nil, "", "")
	if err != nil || c.ProfileID != profiles.BuiltinID || c.Provider != models.ProviderClaude {
		t.Fatalf("default chat = %+v %v", c, err)
	}
	if err := ps.SetDefault(ctx, testUser, cx.ID); err != nil {
		t.Fatal(err)
	}
	c, err = m.CreateChat(ctx, testUser, "t", testWS, nil, "", "gpt-6-luna")
	if err != nil || c.ProfileID != cx.ID {
		t.Fatalf("chat on the codex default = %+v %v", c, err)
	}
	// A Claude model (a stream action) doesn't land on the Codex default.
	c, err = m.CreateChat(ctx, testUser, "t", testWS, nil, "", "claude-opus-5-5")
	if err != nil || c.Provider != models.ProviderClaude {
		t.Fatalf("claude-model chat = %+v %v", c, err)
	}
	if _, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "missing", ""); err == nil {
		t.Fatal("an unknown profile must be refused")
	}
}

// A thread's profile can change while it is empty; once it has messages it
// is fixed (the client opens a new thread instead).
func TestThreadProfileLocksOnceItHasMessages(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	ps := withProfiles(t, m)
	ctx := context.Background()
	name, provider := "Gemini", models.ProviderAntigravity
	ag, err := ps.Create(ctx, testUser, profiles.Input{Provider: &provider, Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"`+ag.ID+`"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"provider":"antigravity"`) {
		t.Fatalf("switching an empty thread: %d %s", w.Code, w.Body)
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"builtin"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"profile_id":"builtin"`) {
		t.Fatalf("switching back: %d %s", w.Code, w.Body)
	}
	// testChat has an assistant message.
	if w := patchChat(m, testChat, `{"profile_id":"`+ag.ID+`"}`); w.Code != http.StatusConflict {
		t.Fatalf("switching a thread with messages: %d %s", w.Code, w.Body)
	}
	// Re-selecting its own profile is a no-op, not a conflict.
	if w := patchChat(m, testChat, `{"profile_id":"builtin"}`); w.Code != http.StatusOK {
		t.Fatalf("same profile: %d %s", w.Code, w.Body)
	}
}

func TestValidateModelPerProvider(t *testing.T) {
	high, maxEffort := "high", "max"
	in := startInput{modelID: "gpt-6-luna", thinkingMode: &high}
	if effort, err := validateModel(&in, models.ProviderCodex); err != nil || effort != "high" {
		t.Fatalf("codex: %q %v", effort, err)
	}
	in = startInput{modelID: "gpt-6-luna", thinkingMode: &maxEffort}
	if effort, err := validateModel(&in, models.ProviderCodex); err != nil || effort != "" {
		t.Fatalf("codex max falls back to the default: %q %v", effort, err)
	}
	in = startInput{modelID: "claude-opus-5-5"}
	if _, err := validateModel(&in, models.ProviderCodex); err == nil {
		t.Fatal("a Claude model on a Codex chat must be refused")
	}
	in = startInput{modelID: "claude-opus-5-5", coerceModel: true}
	if _, err := validateModel(&in, models.ProviderAntigravity); err != nil || in.modelID != models.DefaultModel(models.ProviderAntigravity) {
		t.Fatalf("automation model coerced to %q: %v", in.modelID, err)
	}
	in = startInput{modelID: "gpt-6-luna"}
	if _, err := validateModel(&in, models.ProviderClaude); err == nil {
		t.Fatal("a Codex model on a Claude chat must be refused")
	}
}

func TestOtherProviderModel(t *testing.T) {
	for id, want := range map[string]bool{
		"gpt-6-luna":            true,
		"gemini-3.8-flash-high": true,
		"haiku":                 false,
		"":                      false,
	} {
		if got := otherProviderModel(id); got != want {
			t.Errorf("otherProviderModel(%q) = %v, want %v", id, got, want)
		}
	}
}

func TestProfileShell(t *testing.T) {
	cmd := profileShell("mkdir -p x", "codex", []string{"exec", "-"})
	if cmd[0] != "bash" || cmd[3] != "agent" || cmd[4] != "codex" || cmd[5] != "exec" ||
		!strings.Contains(cmd[2], `command -v "$1"`) || !strings.Contains(cmd[2], "mkdir -p x && exec") {
		t.Fatalf("cmd = %q", cmd)
	}
}
