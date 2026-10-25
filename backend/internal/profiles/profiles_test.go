package profiles

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

const testUser = "u1"

func newTestService(t *testing.T) (*Service, *db.DB) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	keys, err := auth.NewKeys(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(d, keys, &config.Config{StoragePath: t.TempDir()}, nil), d
}

func ptr(s string) *string { return &s }

func status(err error) int {
	var he *httpx.Error
	if errors.As(err, &he) {
		return he.Status
	}
	return 0
}

func TestCreateValidatesAndStoresTokenEncrypted(t *testing.T) {
	s, d := newTestService(t)
	ctx := context.Background()
	p, err := s.Create(ctx, testUser, Input{Provider: ptr("claude"), Name: ptr("  Work  "), Token: ptr("sk-ant-oat01-secret-token")})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Work" || p.AuthMode != AuthToken || p.Token != "sk-ant-oat01-secret-token" {
		t.Fatalf("created %+v", p)
	}
	var stored string
	if err := d.QueryRow(`SELECT token FROM agent_profiles WHERE id = ?`, p.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stored, "enc:") || strings.Contains(stored, "secret") {
		t.Fatalf("token stored in the clear: %q", stored)
	}
	got, err := s.Get(ctx, testUser, p.ID)
	if err != nil || got.Token != "sk-ant-oat01-secret-token" {
		t.Fatalf("get = %+v, %v", got, err)
	}

	for name, in := range map[string]Input{
		"unknown provider":   {Provider: ptr("cursor"), Name: ptr("x")},
		"empty name":         {Provider: ptr("claude"), Name: ptr(" ")},
		"long name":          {Provider: ptr("claude"), Name: ptr(strings.Repeat("n", 61))},
		"login for claude":   {Provider: ptr("claude"), Name: ptr("a"), AuthMode: ptr(AuthLogin)},
		"endpoint for codex": {Provider: ptr("codex"), Name: ptr("a"), AuthMode: ptr(AuthEndpoint)},
		"endpoint w/o url":   {Provider: ptr("claude"), Name: ptr("a"), AuthMode: ptr(AuthEndpoint), Token: ptr("t")},
		"endpoint w/o token": {Provider: ptr("claude"), Name: ptr("a"), AuthMode: ptr(AuthEndpoint), BaseURL: ptr("https://gw.example")},
		"bad base url":       {Provider: ptr("claude"), Name: ptr("a"), AuthMode: ptr(AuthEndpoint), BaseURL: ptr("ftp://gw"), Token: ptr("t")},
		"creds in url":       {Provider: ptr("claude"), Name: ptr("a"), AuthMode: ptr(AuthEndpoint), BaseURL: ptr("https://u:p@gw"), Token: ptr("t")},
		"token with space":   {Provider: ptr("claude"), Name: ptr("a"), Token: ptr("a b")},
		"duplicate name":     {Provider: ptr("claude"), Name: ptr("work")},
		"built-in name":      {Provider: ptr("claude"), Name: ptr("Default")},
	} {
		if _, err := s.Create(ctx, testUser, in); err == nil {
			t.Errorf("%s: created, want an error", name)
		}
	}
	// The same name under another provider is fine.
	if _, err := s.Create(ctx, testUser, Input{Provider: ptr("codex"), Name: ptr("Work")}); err != nil {
		t.Fatalf("codex Work: %v", err)
	}
}

func TestUpdateKeepsTokenUnlessReplacedOrCleared(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	p, err := s.Create(ctx, testUser, Input{Provider: ptr("claude"), Name: ptr("GW"), AuthMode: ptr(AuthEndpoint),
		BaseURL: ptr("https://gw.example/"), UsageURL: ptr("https://gw.example/usage"), Token: ptr("tok1")})
	if err != nil {
		t.Fatal(err)
	}
	if p.BaseURL != "https://gw.example" {
		t.Fatalf("base url not trimmed: %q", p.BaseURL)
	}
	p, err = s.Update(ctx, testUser, p.ID, Input{Name: ptr("Gateway"), Token: ptr("")})
	if err != nil || p.Token != "tok1" || p.Name != "Gateway" {
		t.Fatalf("blank token must keep the old one: %+v %v", p, err)
	}
	p, err = s.Update(ctx, testUser, p.ID, Input{Token: ptr("tok2")})
	if err != nil || p.Token != "tok2" {
		t.Fatalf("replace token: %+v %v", p, err)
	}
	// Switching to a plain token drops the endpoint URLs.
	p, err = s.Update(ctx, testUser, p.ID, Input{AuthMode: ptr(AuthToken)})
	if err != nil || p.BaseURL != "" || p.UsageURL != "" || p.Token != "tok2" {
		t.Fatalf("mode switch: %+v %v", p, err)
	}
	p, err = s.Update(ctx, testUser, p.ID, Input{ClearToken: true})
	if err != nil || p.Token != "" {
		t.Fatalf("clear token: %+v %v", p, err)
	}
	if _, err := s.Update(ctx, testUser, p.ID, Input{Provider: ptr("codex")}); err == nil {
		t.Fatal("changing the provider must fail")
	}
	if _, err := s.Update(ctx, testUser, BuiltinID, Input{Name: ptr("x")}); err == nil {
		t.Fatal("the built-in profile must not be editable")
	}
	if _, err := s.Update(ctx, "someone-else", p.ID, Input{Name: ptr("x")}); status(err) != 404 {
		t.Fatalf("another user's profile: %v", err)
	}
}

func TestDefaultFollowsSetDefaultAndDelete(t *testing.T) {
	s, d := newTestService(t)
	ctx := context.Background()
	if got := s.Default(ctx, testUser); !got.Builtin {
		t.Fatalf("default without settings = %+v", got)
	}
	cx, err := s.Create(ctx, testUser, Input{Provider: ptr("codex"), Name: ptr("Codex")})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefault(ctx, testUser, cx.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.Default(ctx, testUser); got.ID != cx.ID {
		t.Fatalf("default = %s, want %s", got.ID, cx.ID)
	}
	// Claude-only work never runs on a Codex default.
	if got := s.DefaultClaude(ctx, testUser); !got.Builtin {
		t.Fatalf("default claude = %+v", got)
	}
	if err := s.SetDefault(ctx, testUser, "nope"); status(err) != 404 {
		t.Fatalf("unknown default: %v", err)
	}
	if err := s.Delete(ctx, testUser, BuiltinID); err == nil {
		t.Fatal("the built-in profile must not be deletable")
	}
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, workspace_path, created_at, updated_at) VALUES ('w', 'w', ?, 'sb', '/tmp', ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO chats (id, title, user_id, workspace_id, profile_id, provider, created_at, updated_at) VALUES ('c', 'c', ?, 'w', ?, 'codex', ?, ?)`, testUser, cx.ID, now, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, testUser, cx.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.DefaultID(ctx, testUser); got != BuiltinID {
		t.Fatalf("default after delete = %s", got)
	}
	if _, err := s.ForChat(ctx, testUser, &cx.ID); status(err) != 400 || !strings.Contains(err.Error(), "deleted") {
		t.Fatalf("chat on a deleted profile: %v", err)
	}
	if p, err := s.ForChat(ctx, testUser, nil); err != nil || !p.Builtin {
		t.Fatalf("chat without a profile: %+v %v", p, err)
	}
	list, err := s.List(ctx, testUser)
	if err != nil || len(list) != 1 || !list[0].Builtin {
		t.Fatalf("list = %v %v", list, err)
	}
}

func TestLaunchIsolatesCredentials(t *testing.T) {
	work := &Profile{ID: "p1", Provider: models.ProviderClaude, AuthMode: AuthToken, Token: "sk-ant-oat01-abc"}
	l := work.Launch()
	if l.Env["CLAUDE_CONFIG_DIR"] != "/home/user/.agentrove/profiles/p1/claude" || l.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-abc" {
		t.Fatalf("claude token env = %v", l.Env)
	}
	for _, k := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_USAGE_URL"} {
		if !slices.Contains(l.Unset, k) {
			t.Errorf("%s is inherited by a Claude profile", k)
		}
	}
	if !strings.Contains(l.Setup, "ln -s '/home/user/claude/skills' '/home/user/.agentrove/profiles/p1/claude/skills'") {
		t.Fatalf("global skills not linked: %s", l.Setup)
	}
	apiKey := &Profile{ID: "p2", Provider: models.ProviderClaude, AuthMode: AuthToken, Token: "sk-ant-api03-x"}
	if env := apiKey.Launch().Env; env["ANTHROPIC_API_KEY"] != "sk-ant-api03-x" || env["CLAUDE_CODE_OAUTH_TOKEN"] != "" {
		t.Fatalf("api key env = %v", env)
	}
	gw := &Profile{ID: "p3", Provider: models.ProviderClaude, AuthMode: AuthEndpoint, BaseURL: "https://gw", UsageURL: "https://gw/u", Token: "t"}
	if env := gw.Launch().Env; env["ANTHROPIC_BASE_URL"] != "https://gw" || env["ANTHROPIC_AUTH_TOKEN"] != "t" || env["CLAUDE_USAGE_URL"] != "https://gw/u" {
		t.Fatalf("endpoint env = %v", env)
	}
	cx := &Profile{ID: "p4", Provider: models.ProviderCodex, AuthMode: AuthLogin}
	if l := cx.Launch(); l.Env["CODEX_HOME"] != "/home/user/.agentrove/profiles/p4/codex" || !slices.Contains(l.Unset, "OPENAI_API_KEY") || l.Env["CODEX_API_KEY"] != "" {
		t.Fatalf("codex login launch = %+v", l)
	}
	cxKey := &Profile{ID: "p5", Provider: models.ProviderCodex, AuthMode: AuthToken, Token: "sk-proj-1"}
	if l := cxKey.Launch(); l.Env["CODEX_API_KEY"] != "sk-proj-1" {
		t.Fatalf("codex key launch = %+v", l)
	}
	agy := &Profile{ID: "p6", Provider: models.ProviderAntigravity, AuthMode: AuthLogin}
	if l := agy.Launch(); l.Env["HOME"] != "/home/user/.agentrove/profiles/p6/home" || !strings.Contains(l.Setup, ".gitconfig") {
		t.Fatalf("antigravity launch = %+v", l)
	}
	if l := Builtin(testUser).Launch(); len(l.Env) != 0 || len(l.Unset) != 0 || l.Setup != "" {
		t.Fatalf("built-in launch = %+v", l)
	}
	// One-shot runs on the host: the env loses foreign credentials too.
	env := work.ApplyEnv(map[string]string{"ANTHROPIC_API_KEY": "server", "GITHUB_TOKEN": "g"})
	if !reflect.DeepEqual(env, map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-abc", "GITHUB_TOKEN": "g"}) {
		t.Fatalf("apply env = %v", env)
	}
}

func TestMigrateEnvVars(t *testing.T) {
	s, d := newTestService(t)
	ctx := context.Background()
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO user_settings (id, user_id, title_model_id, custom_env_vars, created_at, updated_at) VALUES ('s', ?, 'haiku', ?, ?, ?)`, testUser,
		`[{"key":"ANTHROPIC_BASE_URL","value":"https://gw.example"},{"key":"ANTHROPIC_AUTH_TOKEN","value":"gw-token"},{"key":"CLAUDE_USAGE_URL","value":"https://admin.example/api/usage"},{"key":"DATABASE_URL","value":"pg://"}]`, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO workspaces (id, name, user_id, sandbox_id, workspace_path, created_at, updated_at) VALUES ('w', 'w', ?, 'sb', '/tmp', ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO chats (id, title, user_id, workspace_id, created_at, updated_at) VALUES ('c', 'c', ?, 'w', ?, ?)`, testUser, now, now); err != nil {
		t.Fatal(err)
	}
	s.MigrateEnvVars(ctx)
	s.MigrateEnvVars(ctx) // idempotent

	list, err := s.List(ctx, testUser)
	if err != nil || len(list) != 2 {
		t.Fatalf("profiles = %v %v", list, err)
	}
	p := list[1]
	if p.AuthMode != AuthEndpoint || p.BaseURL != "https://gw.example" || p.Token != "gw-token" || p.UsageURL != "https://admin.example/api/usage" {
		t.Fatalf("migrated profile = %+v", p)
	}
	if p.ConfigDir() != "/home/user/claude" {
		t.Fatalf("migrated profile must keep ~/claude for its chats' sessions, got %s", p.ConfigDir())
	}
	if s.DefaultID(ctx, testUser) != p.ID {
		t.Fatal("migrated profile is not the default")
	}
	var vars, chatProfile string
	if err := d.QueryRow(`SELECT custom_env_vars FROM user_settings WHERE user_id = ?`, testUser).Scan(&vars); err != nil {
		t.Fatal(err)
	}
	if vars != `[{"key":"DATABASE_URL","value":"pg://"}]` {
		t.Fatalf("env vars left = %s", vars)
	}
	if err := d.QueryRow(`SELECT profile_id FROM chats WHERE id = 'c'`).Scan(&chatProfile); err != nil || chatProfile != p.ID {
		t.Fatalf("chat profile = %q %v", chatProfile, err)
	}
}

func TestProfileFromEnvPrefersOAuthToken(t *testing.T) {
	p := profileFromEnv(testUser, []models.CustomEnvVar{{Key: "ANTHROPIC_API_KEY", Value: "key"}, {Key: "CLAUDE_CODE_OAUTH_TOKEN", Value: "sk-ant-oat01-x"}})
	if p == nil || p.AuthMode != AuthToken || p.Token != "sk-ant-oat01-x" {
		t.Fatalf("profile = %+v", p)
	}
	if profileFromEnv(testUser, []models.CustomEnvVar{{Key: "CLAUDE_USAGE_URL", Value: "https://x"}}) != nil {
		t.Fatal("no credentials must make no profile")
	}
}

func TestStatusReadsHostLogins(t *testing.T) {
	s, _ := newTestService(t)
	ctx := context.Background()
	home := s.cfg.AgentHomeDir(testUser)
	write := func(rel, data string) {
		full := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	b := Builtin(testUser)
	if st := s.Status(ctx, b, nil); st.State != StatusSignIn {
		t.Fatalf("built-in without creds = %+v", st)
	}
	if st := s.Status(ctx, b, map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "x"}); st.State != StatusReady {
		t.Fatalf("built-in with server token = %+v", st)
	}
	write("claude/.credentials.json", `{"claudeAiOauth":{}}`)
	write("claude/.claude.json", `{"oauthAccount":{"emailAddress":"me@x.dev","organizationType":"claude_max"}}`)
	if st := s.Status(ctx, b, nil); st.State != StatusReady || st.Account == nil || st.Account.Email != "me@x.dev" || st.Account.Plan != "Max" {
		t.Fatalf("built-in with box login = %+v", st)
	}

	cx := &Profile{ID: "cx", UserID: testUser, Provider: models.ProviderCodex, AuthMode: AuthLogin}
	if st := s.Status(ctx, cx, nil); st.State != StatusSignIn {
		t.Fatalf("codex without login = %+v", st)
	}
	claims := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"dev@openai.example","https://api.openai.com/auth":{"chatgpt_plan_type":"pro"}}`))
	write(".agentrove/profiles/cx/codex/auth.json", `{"tokens":{"id_token":"h.`+claims+`.s"}}`)
	if st := s.Status(ctx, cx, nil); st.State != StatusReady || st.Account == nil || st.Account.Email != "dev@openai.example" || st.Account.Plan != "Pro" {
		t.Fatalf("codex signed in = %+v", st)
	}

	agy := &Profile{ID: "ag", UserID: testUser, Provider: models.ProviderAntigravity, AuthMode: AuthLogin}
	if st := s.Status(ctx, agy, nil); st.State != StatusSignIn {
		t.Fatalf("agy without login = %+v", st)
	}
	write(".agentrove/profiles/ag/home/.gemini/antigravity-cli/antigravity-oauth-token", "tok")
	write(".agentrove/profiles/ag/home/.gemini/google_accounts.json", `{"active":"g@example.com"}`)
	if st := s.Status(ctx, agy, nil); st.State != StatusReady || st.Account == nil || st.Account.Email != "g@example.com" {
		t.Fatalf("agy signed in = %+v", st)
	}

	// A login file planted as a symlink out of the home doesn't count.
	outside := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(outside, []byte(`{"OPENAI_API_KEY":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	evil := &Profile{ID: "ev", UserID: testUser, Provider: models.ProviderCodex, AuthMode: AuthLogin}
	dir := filepath.Join(home, ".agentrove/profiles/ev/codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "auth.json")); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(ctx, evil, nil); st.State != StatusSignIn {
		t.Fatalf("symlinked login = %+v", st)
	}

	s.NoteMissing(testUser, "codex")
	if st := s.Status(ctx, cx, nil); st.State != StatusCLIMissing {
		t.Fatalf("missing CLI = %+v", st)
	}
}

func TestLoginOutputParsing(t *testing.T) {
	raw := "\x1b[2J\x1b[1mOpening browser…\x1b[0m\r\nVisit \x1b]8;;https://claude.ai/oauth/authorize?code=true&client_id=abc\x07link\x1b]8;;\x07 to sign in.\r\n" +
		"See https://docs.example/help for help.\r\nProgress 10%\rProgress 100%\r\nPaste code here if prompted > "
	text := cleanTerminal(raw)
	if strings.Contains(text, "\x1b") || !strings.Contains(text, "Progress 100%") || strings.Contains(text, "Progress 10%P") {
		t.Fatalf("cleaned = %q", text)
	}
	if got := pickURL(urlRE.FindAllString(raw, -1)); got != "https://claude.ai/oauth/authorize?code=true&client_id=abc" {
		t.Fatalf("url = %q", got)
	}
	if got := deviceCodeRE.FindString("Enter this one-time code: ABCD-12345\n"); got != "ABCD-12345" {
		t.Fatalf("device code = %q", got)
	}
	if m := claudeTokenRE.FindStringSubmatch("Your token: sk-ant-oat01-" + strings.Repeat("a", 30)); m != nil {
		t.Fatal("a token not yet followed by whitespace may be cut short")
	}
	if m := claudeTokenRE.FindStringSubmatch("sk-ant-oat01-" + strings.Repeat("b", 30) + "\nStore it"); m == nil || m[1] != "sk-ant-oat01-"+strings.Repeat("b", 30) {
		t.Fatalf("token = %v", m)
	}
	if out := secretRE.ReplaceAllString("token sk-ant-oat01-"+strings.Repeat("c", 30), "sk-ant-…"); strings.Contains(out, "ccc") {
		t.Fatalf("token echoed: %q", out)
	}
	if _, err := loginCommand(&Profile{Provider: models.ProviderClaude, AuthMode: AuthEndpoint}); err == nil {
		t.Fatal("an endpoint profile has no sign-in")
	}
	if cmd, _ := loginCommand(&Profile{Provider: models.ProviderCodex, AuthMode: AuthLogin}); !reflect.DeepEqual(cmd, []string{"codex", "login", "--device-auth"}) {
		t.Fatalf("codex login = %v", cmd)
	}
}
