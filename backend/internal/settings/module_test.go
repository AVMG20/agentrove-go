package settings

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
	"agentrove/internal/store"
)

const userID = "11111111-1111-1111-1111-111111111111"

func TestBrowserSettings(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{APIPrefix: "/api/v1", SessionTTL: time.Hour, BrowserEnabled: true}
	a := &app.App{Cfg: cfg, DB: d, Keys: keys, Store: &store.Store{DB: d, Keys: keys}, Sandboxes: &sandbox.Manager{}}
	m := New(a)
	call := func(method, body string) (int, map[string]any) {
		req := httptest.NewRequest(method, "/api/v1/settings/", strings.NewReader(body))
		req = req.WithContext(auth.WithUser(req.Context(), &models.User{ID: userID}))
		rec := httptest.NewRecorder()
		var err error
		if method == "GET" {
			err = m.get(rec, req)
		} else {
			req.Header.Set("Content-Type", "application/json")
			err = m.patch(rec, req)
		}
		if err != nil {
			return 422, nil
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if _, out := call("GET", ""); out["browser_tools_enabled"] != true || out["browser_available"] != true {
		t.Fatalf("defaults = %v / %v", out["browser_tools_enabled"], out["browser_available"])
	}
	if _, out := call("PATCH", `{"browser_tools_enabled": false}`); out["browser_tools_enabled"] != false {
		t.Fatalf("after patch = %v", out["browser_tools_enabled"])
	}
	if code, _ := call("PATCH", `{"browser_tools_enabled": "no"}`); code != 422 {
		t.Fatalf("non-boolean accepted: %d", code)
	}
	cfg.BrowserEnabled = false
	if _, out := call("GET", ""); out["browser_available"] != false {
		t.Fatalf("browser_available = %v with the browser off", out["browser_available"])
	}
}

// Claude vars a provider profile didn't take over stay in the env vars as
// they were; adding or changing one is refused.
func TestManagedEnvVarsOnlyStayAsTheyWere(t *testing.T) {
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := db.Now()
	if _, err := d.Exec(`INSERT INTO users (id, email, username, hashed_password, is_active, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', 1, ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO user_settings (id, user_id, title_model_id, custom_env_vars, created_at, updated_at) VALUES ('s', ?, 'haiku', ?, ?, ?)`,
		userID, `[{"key":"CLAUDE_USAGE_URL","value":"https://admin.example/api/usage"}]`, now, now); err != nil {
		t.Fatal(err)
	}
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	cfg := &config.Config{APIPrefix: "/api/v1", SessionTTL: time.Hour}
	a := &app.App{Cfg: cfg, DB: d, Keys: keys, Store: &store.Store{DB: d, Keys: keys}, Sandboxes: &sandbox.Manager{}}
	m := New(a)
	patch := func(body string) error {
		req := httptest.NewRequest("PATCH", "/api/v1/settings/", strings.NewReader(body))
		req = req.WithContext(auth.WithUser(req.Context(), &models.User{ID: userID}))
		req.Header.Set("Content-Type", "application/json")
		return m.patch(httptest.NewRecorder(), req)
	}
	kept := `{"key":"CLAUDE_USAGE_URL","value":"https://admin.example/api/usage"}`
	if err := patch(`{"custom_env_vars":[` + kept + `,{"key":"FOO","value":"bar"}]}`); err != nil {
		t.Fatalf("saving with a kept var: %v", err)
	}
	if err := patch(`{"custom_env_vars":[{"key":"CLAUDE_USAGE_URL","value":"https://other.example"}]}`); err == nil {
		t.Fatal("changing a managed var must be refused")
	}
	if err := patch(`{"custom_env_vars":[` + kept + `,{"key":"ANTHROPIC_API_KEY","value":"k"}]}`); err == nil {
		t.Fatal("adding a managed var must be refused")
	}
}
