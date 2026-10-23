package automation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/httpx"
	"agentrove/internal/store"
)

const (
	uid  = "11111111-1111-1111-1111-111111111111"
	wsID = "22222222-2222-2222-2222-222222222222"
)

func newService(t *testing.T) (*Service, *[]app.ChatTurnRequest) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	a := &app.App{
		Cfg: &config.Config{APIPrefix: "/api/v1", BaseURL: "http://localhost:8080"}, DB: d, Keys: keys,
		Hub: events.NewHub(), Store: &store.Store{DB: d, Keys: keys},
	}
	var turns []app.ChatTurnRequest
	a.Hooks.StartChatTurn = func(req app.ChatTurnRequest) (app.ChatTurnResult, error) {
		turns = append(turns, req)
		if req.Prompt == "fail" {
			return app.ChatTurnResult{}, errors.New("boom")
		}
		return app.ChatTurnResult{ChatID: req.ChatID, MessageID: "m"}, nil
	}
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', ?, ?)`, uid, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path, created_at, updated_at)
		VALUES (?, 'ws', ?, 'sb', 'host', '/tmp', ?, ?)`, wsID, uid, now, now); err != nil {
		t.Fatal(err)
	}
	return NewService(a), &turns
}

func sp(s string) *string { return &s }

func status(err error) int {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func TestCreateValidation(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	base := func() *Input {
		return &Input{WorkspaceID: sp(wsID), Name: sp("n"), Prompt: sp("p"), ModelID: sp("sonnet"), CronExpression: sp("0 9 * * *")}
	}
	cases := []struct {
		name   string
		mutate func(*Input)
		want   int
	}{
		{"ok", func(*Input) {}, 0},
		{"bad cron", func(in *Input) { in.CronExpression = sp("nope") }, 422},
		{"bad tz", func(in *Input) { in.Timezone = sp("Nowhere/City") }, 422},
		{"unknown model", func(in *Input) { in.ModelID = sp("gpt-4") }, 400},
		{"github without repo", func(in *Input) { in.TriggerType = sp("github") }, 422},
		{"bad repo", func(in *Input) { in.TriggerType = sp("github"); in.GithubRepo = sp("nope") }, 422},
		{"no future run", func(in *Input) { in.CronExpression = sp("0 0 31 2 *") }, 400},
		{"missing workspace", func(in *Input) { in.WorkspaceID = sp("33333333-3333-3333-3333-333333333333") }, 404},
		{"missing field", func(in *Input) { in.Prompt = nil }, 422},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base()
			c.mutate(in)
			x, err := s.Create(ctx, uid, in, "")
			if got := status(err); got != c.want || (c.want == 0 && err != nil) {
				t.Fatalf("status %d (%v), want %d", got, err, c.want)
			}
			if c.want == 0 && (x.PermissionMode != "bypassPermissions" || x.Timezone != "UTC" || !x.Enabled || x.SelectedPersonaName != "Default") {
				t.Fatalf("defaults: %+v", x)
			}
		})
	}
}

func TestRunAndDispatch(t *testing.T) {
	s, turns := newService(t)
	ctx := context.Background()
	x, err := s.Create(ctx, uid, &Input{WorkspaceID: sp(wsID), Name: sp("Nightly"), Prompt: sp("do it"), ModelID: sp("opus"),
		CronExpression: sp("*/5 * * * *"), Timezone: sp("Europe/Amsterdam")}, "")
	if err != nil {
		t.Fatal(err)
	}
	chatID, err := s.Run(ctx, x.ID, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(*turns) != 1 || !(*turns)[0].Unattended || (*turns)[0].ChatID != chatID || (*turns)[0].Prompt != "do it" {
		t.Fatalf("turns: %+v", *turns)
	}
	chat, err := s.a.Store.GetChat(ctx, uid, chatID)
	if err != nil || !strings.HasPrefix(chat.Title, "Nightly · ") {
		t.Fatalf("chat: %+v %v", chat, err)
	}

	// Make it due; the tick advances next_run_at before firing.
	past := db.T(time.Now().Add(-time.Minute))
	if _, err := s.a.DB.Exec(`UPDATE automations SET next_run_at = ? WHERE id = ?`, past, x.ID); err != nil {
		t.Fatal(err)
	}
	s.RunDue(ctx)
	if len(*turns) != 2 {
		t.Fatalf("due automation did not fire: %d", len(*turns))
	}
	after, _ := s.getByID(ctx, x.ID)
	if !after.NextRunAt.After(time.Now()) || !after.LastRunAt.Valid {
		t.Fatalf("schedule not advanced: %+v", after)
	}
	s.RunDue(ctx)
	if len(*turns) != 2 {
		t.Fatal("fired twice for one slot")
	}

	// A failed start leaves no orphan chat.
	if _, err := s.Update(ctx, x.ID, uid, &Input{Prompt: sp("fail"), Set: map[string]bool{"prompt": true}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Run(ctx, x.ID, uid); err == nil {
		t.Fatal("want error")
	}
	var n int
	_ = s.a.DB.QueryRow(`SELECT COUNT(1) FROM chats`).Scan(&n)
	if n != 2 {
		t.Fatalf("chats = %d, want 2", n)
	}

	// Deleted workspace disables the automation on dispatch.
	if _, err := s.a.DB.Exec(`UPDATE workspaces SET deleted_at = ?`, db.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.DB.Exec(`UPDATE automations SET next_run_at = ?, prompt = 'ok' WHERE id = ?`, past, x.ID); err != nil {
		t.Fatal(err)
	}
	s.RunDue(ctx)
	after, _ = s.getByID(ctx, x.ID)
	if after.Enabled {
		t.Fatal("automation should be disabled after its workspace was deleted")
	}
}

func TestWebhook(t *testing.T) {
	s, _ := newService(t)
	ctx := context.Background()
	x, err := s.Create(ctx, uid, &Input{WorkspaceID: sp(wsID), Name: sp("gh"), Prompt: sp("p"), ModelID: sp("opus"),
		CronExpression: sp("*/10 * * * *"), TriggerType: sp("github"), GithubRepo: sp("octo/repo"),
		GithubEvents: &[]string{"issue_opened", "issue_comment_created"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if x.GithubWebhookActive || x.GithubWebhookError == nil {
		t.Fatalf("localhost should not register a webhook: %+v", x)
	}
	enc, _ := s.a.Keys.Encrypt("whsec")
	if _, err := s.a.DB.Exec(`UPDATE automations SET github_webhook_id = 1, github_webhook_secret = ? WHERE id = ?`, enc, x.ID); err != nil {
		t.Fatal(err)
	}
	body := `{"action":"opened"}`
	cases := []struct {
		name, sig, event, action string
		want                     bool
		code                     int
	}{
		{"relevant", sign("whsec", body), "issues", "opened", true, 0},
		{"other action", sign("whsec", body), "issues", "closed", false, 0},
		{"other event", sign("whsec", body), "pull_request", "opened", false, 0},
		{"bad signature", sign("nope", body), "issues", "opened", false, 404},
	}
	for _, c := range cases {
		got, err := s.HandleWebhook(ctx, x.ID, []byte(body), c.sig, c.event, c.action)
		if got != c.want || status(err) != c.code {
			t.Errorf("%s: got %v, %v", c.name, got, err)
		}
	}
	if _, err := s.HandleWebhook(ctx, "33333333-3333-3333-3333-333333333333", []byte(body), sign("whsec", body), "issues", "opened"); status(err) != 404 {
		t.Errorf("unknown id: %v", err)
	}
}
