package usage

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentrove/internal/models"
)

func TestResolveSource(t *testing.T) {
	// A custom base URL gets <base>/api/usage; AUTH_TOKEN wins over API_KEY.
	global := map[string]string{"ANTHROPIC_BASE_URL": "https://Claude.Bitsec.dev/", "ANTHROPIC_API_KEY": "k1"}
	s := ResolveSource(global, nil)
	if s == nil || s.Provider != "claude.bitsec.dev" || s.Token != "k1" || s.URL != "https://Claude.Bitsec.dev/api/usage" || s.Headers != nil {
		t.Fatalf("proxy = %+v", s)
	}
	// User vars override the deployment env.
	s = ResolveSource(global, []models.CustomEnvVar{{Key: "ANTHROPIC_AUTH_TOKEN", Value: "t2"}})
	if s == nil || s.Token != "t2" {
		t.Fatalf("override = %+v", s)
	}
	s = ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example:8443/v1", "ANTHROPIC_AUTH_TOKEN": "k"}, nil)
	if s == nil || s.URL != "https://proxy.example:8443/v1/api/usage" {
		t.Fatalf("base path = %+v", s)
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com"}, nil) != nil {
		t.Error("missing token should be unavailable")
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "http://plain.example", "ANTHROPIC_API_KEY": "k"}, nil) != nil {
		t.Error("http base URL must not get an endpoint")
	}
	// A proxy never gets the OAuth token meant for Anthropic.
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com", "CLAUDE_CODE_OAUTH_TOKEN": "o"}, nil) != nil {
		t.Error("OAuth token must not go to a proxy")
	}

	// No base URL (or Anthropic's own) uses the official endpoint; the OAuth
	// token wins there.
	official := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth", "ANTHROPIC_AUTH_TOKEN": "auth"}
	for _, base := range []string{"", "https://api.anthropic.com", "https://API.anthropic.com/v1/"} {
		env := map[string]string{"ANTHROPIC_BASE_URL": base}
		for k, v := range official {
			env[k] = v
		}
		s = ResolveSource(env, nil)
		if s == nil || s.URL != "https://api.anthropic.com/api/oauth/usage" || s.Token != "oauth" || s.Provider != "api.anthropic.com" ||
			s.Headers["anthropic-beta"] != "oauth-2025-04-20" {
			t.Fatalf("official (%q) = %+v", base, s)
		}
	}
	// Only an OAuth token goes to the official endpoint; an API key never works there.
	if s = ResolveSource(map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api03-x"}, nil); s != nil {
		t.Fatalf("API key on official = %+v", s)
	}
	s = ResolveSource(map[string]string{"ANTHROPIC_AUTH_TOKEN": "sk-ant-oat01-x"}, nil)
	if s == nil || s.Token != "sk-ant-oat01-x" {
		t.Fatalf("official OAuth fallback = %+v", s)
	}
	if ResolveSource(map[string]string{}, nil) != nil {
		t.Error("no token should be unavailable")
	}
	// The user's own base URL beats the deployment's official default.
	s = ResolveSource(official, []models.CustomEnvVar{{Key: "ANTHROPIC_BASE_URL", Value: "https://proxy.example.com"}})
	if s == nil || s.URL != "https://proxy.example.com/api/usage" || s.Token != "auth" || !s.PublicOnly {
		t.Fatalf("user proxy = %+v", s)
	}
	// A deployment-set proxy may be internal.
	if s = ResolveSource(global, nil); s == nil || s.PublicOnly {
		t.Fatalf("deployment proxy = %+v", s)
	}
}

func TestResolveSourceUsageURL(t *testing.T) {
	bitsec := map[string]string{"ANTHROPIC_BASE_URL": "https://claude.bitsec.dev", "ANTHROPIC_AUTH_TOKEN": "b"}
	custom := []models.CustomEnvVar{{Key: "CLAUDE_USAGE_URL", Value: "https://claude-admin.bitsec.dev/api/usage"}}
	s := ResolveSource(bitsec, custom)
	if s == nil || s.URL != "https://claude-admin.bitsec.dev/api/usage" || s.Token != "b" || s.Provider != "claude.bitsec.dev" || !s.PublicOnly {
		t.Fatalf("user usage URL = %+v", s)
	}
	// Set by the deployment (AGENT_ENV), it may point inside.
	bitsec["CLAUDE_USAGE_URL"] = "https://usage.internal/api/usage"
	if s = ResolveSource(bitsec, nil); s == nil || s.URL != "https://usage.internal/api/usage" || s.PublicOnly {
		t.Fatalf("deployment usage URL = %+v", s)
	}
	// A broken override is unavailable rather than silently ignored.
	for _, bad := range []string{"http://claude-admin.bitsec.dev/api/usage", "not a url", "https://"} {
		bitsec["CLAUDE_USAGE_URL"] = bad
		if s = ResolveSource(bitsec, nil); s != nil {
			t.Errorf("%q = %+v", bad, s)
		}
	}
	// Still needs a token.
	if s = ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://claude.bitsec.dev"}, custom); s != nil {
		t.Errorf("no token = %+v", s)
	}
}

func TestPublicOnlyControl(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:443", "10.0.0.5:443", "172.18.0.2:443", "192.168.1.1:443", "169.254.169.254:80", "[::1]:443", "[fd00::1]:443", "0.0.0.0:443"} {
		if publicOnlyControl("tcp", addr, nil) == nil {
			t.Errorf("%s should be refused", addr)
		}
	}
	for _, addr := range []string{"1.1.1.1:443", "[2606:4700::1111]:443"} {
		if err := publicOnlyControl("tcp", addr, nil); err != nil {
			t.Errorf("%s: %v", addr, err)
		}
	}
	m := New(nil)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10}}`))
	}))
	defer srv.Close()
	if r := m.fetch(context.Background(), &Source{Provider: "p", URL: srv.URL, Token: "t", PublicOnly: true}); r.Status != "error" {
		t.Fatalf("loopback fetch with PublicOnly = %+v", r)
	}
}

func TestParseBody(t *testing.T) {
	var body any
	_ = json.Unmarshal([]byte(`{"five_hour":{"utilization":42.5,"resets_at":"2026-01-02T03:04:05Z"},
		"seven_day":{"usedPercent":150,"resetsAt":1767323045000}}`), &body)
	r := ParseBody("p", body)
	if r.Status != "ok" || len(r.Windows) != 2 {
		t.Fatalf("resp = %+v", r)
	}
	session, weekly := r.Windows[0], r.Windows[1]
	if session.UsedPercent != 42.5 || session.WindowSeconds != 18000 || weekly.UsedPercent != 100 || weekly.WindowSeconds != 604800 {
		t.Fatalf("windows = %+v", r.Windows)
	}
	if !session.ResetsAt.Valid || !session.ResetsAt.Time.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("session reset = %+v", session.ResetsAt)
	}
	if !weekly.ResetsAt.Valid || weekly.ResetsAt.Time.Unix() != 1767323045 {
		t.Errorf("weekly reset = %+v", weekly.ResetsAt)
	}
	_ = json.Unmarshal([]byte(`{"five_hour":null,"seven_day":{"utilization":5}}`), &body)
	if r := ParseBody("p", body); r.Status != "ok" || len(r.Windows) != 1 || r.Windows[0].WindowSeconds != 604800 {
		t.Errorf("weekly only: %+v", r)
	}
	if r := ParseBody("p", []any{}); r.Status != "error" {
		t.Errorf("non-object: %+v", r)
	}
	_ = json.Unmarshal([]byte(`{"five_hour":{"utilization":true}}`), &body)
	if r := ParseBody("p", body); r.Status != "error" {
		t.Errorf("no windows: %+v", r)
	}
}

func TestParseReset(t *testing.T) {
	for _, v := range []any{nil, true, -1.0, "", "nonsense"} {
		if parseReset(v).Valid {
			t.Errorf("parseReset(%v) should be null", v)
		}
	}
	if r := parseReset("1767323045"); !r.Valid || r.Time.Unix() != 1767323045 {
		t.Errorf("numeric string: %+v", r)
	}
}

func TestFetchSendsTokenAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":10}}`))
	}))
	defer srv.Close()
	m := &Module{client: srv.Client()}
	r := m.fetch(context.Background(), &Source{Provider: "p", URL: srv.URL, Token: "tok", Headers: map[string]string{"anthropic-beta": "oauth-2025-04-20"}})
	if r.Status != "ok" || len(r.Windows) != 1 || r.Windows[0].UsedPercent != 10 {
		t.Fatalf("fetch = %+v", r)
	}
}

func TestParseCodexBody(t *testing.T) {
	var body any
	_ = json.Unmarshal([]byte(`{"plan_type":"plus","rate_limit":{"allowed":true,"primary_window":{"used_percent":0,"limit_window_seconds":2592000,"reset_after_seconds":2591999,"reset_at":1795545771},"secondary_window":null}}`), &body)
	r := ParseCodexBody("chatgpt.com", body)
	if r.Status != "ok" || r.Provider == nil || *r.Provider != "chatgpt.com" || len(r.Windows) != 1 {
		t.Fatalf("resp = %+v", r)
	}
	if w := r.Windows[0]; w.UsedPercent != 0 || w.WindowSeconds != 2592000 || !w.ResetsAt.Valid || w.ResetsAt.Time.Unix() != 1795545771 {
		t.Fatalf("window = %+v", w)
	}
	// Both windows come shortest first, whichever is primary.
	_ = json.Unmarshal([]byte(`{"rate_limit":{"primary_window":{"used_percent":12.5,"limit_window_seconds":604800,"reset_at":1795545771},
		"secondary_window":{"used_percent":140,"limit_window_seconds":18000,"reset_at":1795000000}}}`), &body)
	r = ParseCodexBody("chatgpt.com", body)
	if r.Status != "ok" || len(r.Windows) != 2 || r.Windows[0].WindowSeconds != 18000 || r.Windows[0].UsedPercent != 100 ||
		r.Windows[1].WindowSeconds != 604800 || r.Windows[1].UsedPercent != 12.5 {
		t.Fatalf("two windows = %+v", r.Windows)
	}
	for name, raw := range map[string]string{
		"no rate limit":   `{"plan_type":"free"}`,
		"null windows":    `{"rate_limit":{"primary_window":null,"secondary_window":null}}`,
		"no used percent": `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`,
	} {
		_ = json.Unmarshal([]byte(raw), &body)
		if r := ParseCodexBody("chatgpt.com", body); r.Status != "error" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := ParseCodexBody("chatgpt.com", []any{}); r.Status != "error" {
		t.Errorf("non-object: %+v", r)
	}
}

func TestFetchCodexLayout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at" || r.Header.Get("ChatGPT-Account-Id") != "acc" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":30,"limit_window_seconds":18000,"reset_at":1795545771}}}`))
	}))
	defer srv.Close()
	m := &Module{client: srv.Client()}
	r := m.fetch(context.Background(), &Source{Codex: true, Provider: "chatgpt.com", URL: srv.URL, Token: "at", Headers: map[string]string{"ChatGPT-Account-Id": "acc"}})
	if r.Status != "ok" || len(r.Windows) != 1 || r.Windows[0].UsedPercent != 30 || r.Windows[0].WindowSeconds != 18000 {
		t.Fatalf("fetch = %+v", r)
	}
}

func TestParseAgyBody(t *testing.T) {
	var body any
	raw := `{"status":"SUCCESS","command":{"name":"usage","data":{"groups":[
		{"name":"Gemini Models","buckets":[
			{"id":"gemini-weekly","window":"weekly","remaining_fraction":0.9717,"reset_time":"2026-11-01T11:14:08Z"},
			{"id":"gemini-5h","window":"5h","remaining_fraction":0.25,"reset_time":"2026-10-26T15:00:00Z"}]},
		{"name":"Claude and GPT models","buckets":[{"id":"3p-weekly","window":"weekly","remaining_fraction":1}]}]}}}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	res := ParseAgyBody(body)
	if res.Status != "ok" || len(res.Windows) != 2 {
		t.Fatalf("res = %+v", res)
	}
	session, weekly := res.Windows[0], res.Windows[1]
	if session.WindowSeconds != sessionSeconds || session.UsedPercent != 75 || !session.ResetsAt.Valid {
		t.Fatalf("session = %+v", session)
	}
	if weekly.WindowSeconds != weekSeconds || math.Abs(weekly.UsedPercent-2.83) > 0.01 {
		t.Fatalf("weekly = %+v", weekly)
	}

	if res := ParseAgyBody(map[string]any{}); res.Status != "error" {
		t.Fatalf("empty = %+v", res)
	}
	for window, want := range map[string]int{"5h": sessionSeconds, "5_hour": sessionSeconds, "five_hour": sessionSeconds, "daily": daySeconds, "WEEKLY": weekSeconds, "monthly": 0} {
		if got := agyWindowSeconds(window); got != want {
			t.Errorf("agyWindowSeconds(%q) = %d, want %d", window, got, want)
		}
	}
}
