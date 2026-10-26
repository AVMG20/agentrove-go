package chat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
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

// addProfile adds an account of provider to the test user.
func addProfile(t *testing.T, ps *profiles.Service, provider, name string) *profiles.Profile {
	t.Helper()
	in := profiles.Input{Provider: &provider, Name: &name}
	if provider == models.ProviderClaude {
		mode, tok := profiles.AuthToken, "sk-ant-oat01-"+name
		in.AuthMode, in.Token = &mode, &tok
	}
	p, err := ps.Create(context.Background(), testUser, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// settleQueued lets the queue check a profile switch starts in the
// background (runQueued) finish before the test's DB is closed and removed.
func settleQueued(t *testing.T, d *db.DB) {
	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
		waitFor(t, 2*time.Second, "background queries", func() bool { return d.Stats().InUse == 0 })
	})
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
	// Without an account no thread can start.
	if _, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "", ""); !errors.Is(err, profiles.ErrNoProfile) {
		t.Fatalf("chat without accounts: %v", err)
	}
	work := addProfile(t, ps, models.ProviderClaude, "Work")
	cx := addProfile(t, ps, models.ProviderCodex, "Codex")

	c, err := m.CreateChat(ctx, testUser, "t", testWS, nil, cx.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.ProfileID != cx.ID || c.Provider != models.ProviderCodex {
		t.Fatalf("chat profile = %s/%s", c.ProfileID, c.Provider)
	}

	// No profile: the default (the oldest until set).
	c, err = m.CreateChat(ctx, testUser, "t", testWS, nil, "", "")
	if err != nil || c.ProfileID != work.ID || c.Provider != models.ProviderClaude {
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
	if err != nil || c.ProfileID != work.ID || c.Provider != models.ProviderClaude {
		t.Fatalf("claude-model chat = %+v %v", c, err)
	}
	// Without an account of the model's provider, the default stays.
	c, err = m.CreateChat(ctx, testUser, "t", testWS, nil, "", "gemini-3.8-flash-high")
	if err != nil || c.ProfileID != cx.ID {
		t.Fatalf("antigravity-model chat = %+v %v", c, err)
	}
	if _, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "missing", ""); err == nil {
		t.Fatal("an unknown profile must be refused")
	}
}

// A thread from before accounts (no profile) asks for a new thread.
func TestChatWithoutAProfileAsksForANewThread(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	ps := withProfiles(t, m)
	addProfile(t, ps, models.ProviderClaude, "Work")
	c, err := m.a.Store.GetChat(context.Background(), testUser, testChat)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProfileID != "" || c.Provider != models.ProviderClaude {
		t.Fatalf("legacy chat = %q/%q", c.ProfileID, c.Provider)
	}
	if _, err := m.chatProfile(context.Background(), testUser, c); err == nil || !strings.Contains(err.Error(), "no account") {
		t.Fatalf("chat profile = %v", err)
	}
}

// Titles and prompt enhancing run on Claude: without a Claude account they
// say so instead of running on anything else.
func TestGenerateTextNeedsAClaudeAccount(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	ps := withProfiles(t, m)
	ctx := context.Background()
	if _, err := m.generateText(ctx, testUser, "", "sys", "hi"); !errors.Is(err, errNoClaude) {
		t.Fatalf("no accounts: %v", err)
	}
	addProfile(t, ps, models.ProviderCodex, "Codex")
	if _, err := m.generateText(ctx, testUser, "gpt-6-luna", "sys", "hi"); !errors.Is(err, errNoClaude) {
		t.Fatalf("codex only: %v", err)
	}
}

// A thread's profile can change while it is empty; once it has messages it
// is fixed (the client opens a new thread instead).
func TestThreadProfileLocksOnceItHasMessages(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	settleQueued(t, d)
	ps := withProfiles(t, m)
	ctx := context.Background()
	work := addProfile(t, ps, models.ProviderClaude, "Work")
	ag := addProfile(t, ps, models.ProviderAntigravity, "Gemini")
	empty, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"`+ag.ID+`"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"provider":"antigravity"`) {
		t.Fatalf("switching an empty thread: %d %s", w.Code, w.Body)
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"`+work.ID+`"}`); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"profile_id":"`+work.ID+`"`) {
		t.Fatalf("switching back: %d %s", w.Code, w.Body)
	}
	// testChat has an assistant message.
	if _, err := d.Exec(`UPDATE chats SET profile_id = ?, provider = 'claude' WHERE id = ?`, work.ID, testChat); err != nil {
		t.Fatal(err)
	}
	if w := patchChat(m, testChat, `{"profile_id":"`+ag.ID+`"}`); w.Code != http.StatusConflict {
		t.Fatalf("switching a thread with messages: %d %s", w.Code, w.Body)
	}
	// Re-selecting its own profile is a no-op, not a conflict.
	if w := patchChat(m, testChat, `{"profile_id":"`+work.ID+`"}`); w.Code != http.StatusOK {
		t.Fatalf("same profile: %d %s", w.Code, w.Body)
	}
}

// A switch must not race a turn that is starting: it takes the turn slot,
// and the UPDATE itself only lands on a thread that is still empty.
func TestThreadProfileSwitchRacesNoTurn(t *testing.T) {
	m, d := newTestModule(t)
	withApp(m, d)
	settleQueued(t, d)
	ps := withProfiles(t, m)
	ctx := context.Background()
	work := addProfile(t, ps, models.ProviderClaude, "Work")
	ag := addProfile(t, ps, models.ProviderAntigravity, "Gemini")
	empty, err := m.CreateChat(ctx, testUser, "t", testWS, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// A turn holds the slot (it has reserved and not yet written its rows).
	if !m.reserve(empty.ID) {
		t.Fatal("reserve")
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"`+ag.ID+`"}`); w.Code != http.StatusConflict {
		t.Fatalf("switch during a starting turn: %d %s", w.Code, w.Body)
	}
	m.unreserve(empty.ID)

	// The turn got as far as a session: the conditional UPDATE refuses.
	if _, err := d.Exec(`UPDATE chats SET session_id = 'sess' WHERE id = ?`, empty.ID); err != nil {
		t.Fatal(err)
	}
	if w := patchChat(m, empty.ID, `{"profile_id":"`+ag.ID+`","title":"renamed"}`); w.Code != http.StatusConflict {
		t.Fatalf("switch after a session started: %d %s", w.Code, w.Body)
	}
	var profileID *string
	var title string
	if err := d.QueryRow(`SELECT profile_id, title FROM chats WHERE id = ?`, empty.ID).Scan(&profileID, &title); err != nil {
		t.Fatal(err)
	}
	if db.Deref(profileID) != work.ID || title != "t" {
		t.Fatalf("a refused switch changed the chat: %v %q", profileID, title)
	}
	// The slot is released again.
	if !m.reserve(empty.ID) {
		t.Fatal("the switch kept the turn slot")
	}
	m.unreserve(empty.ID)
}

func TestAgyAuthFailureGetsTheSignInHint(t *testing.T) {
	if !looksUnauthenticated("authentication failed or timed out") {
		t.Fatal("agy's auth failure must count as unauthenticated")
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
