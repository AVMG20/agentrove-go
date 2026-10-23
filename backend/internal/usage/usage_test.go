package usage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentrove/internal/models"
)

func TestResolveSource(t *testing.T) {
	endpoints := map[string]string{"Proxy.Example.com": "https://proxy.example.com/usage", "insecure.example": "http://x"}
	global := map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com/v1", "ANTHROPIC_API_KEY": "k1"}
	s := ResolveSource(global, endpoints, nil)
	if s == nil || s.Provider != "proxy.example.com" || s.Token != "k1" || s.URL != "https://proxy.example.com/usage" || s.Headers != nil {
		t.Fatalf("source = %+v", s)
	}
	// User vars override the deployment env; AUTH_TOKEN wins over API_KEY.
	s = ResolveSource(global, endpoints, []models.CustomEnvVar{{Key: "ANTHROPIC_AUTH_TOKEN", Value: "t2"}})
	if s == nil || s.Token != "t2" {
		t.Fatalf("override = %+v", s)
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com"}, endpoints, nil) != nil {
		t.Error("missing token should be unavailable")
	}

	// Unmapped proxies get <origin>/api/oauth/usage, https only.
	s = ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://Other.example:8443/v1", "ANTHROPIC_API_KEY": "k"}, endpoints, nil)
	if s == nil || s.URL != "https://Other.example:8443/api/oauth/usage" || s.Provider != "other.example" || s.Headers != nil {
		t.Fatalf("derived = %+v", s)
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "http://plain.example", "ANTHROPIC_API_KEY": "k"}, endpoints, nil) != nil {
		t.Error("http base URL must not derive an endpoint")
	}
	// A non-https mapping is ignored, so the host falls back to deriving.
	s = ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://insecure.example", "ANTHROPIC_API_KEY": "k"}, endpoints, nil)
	if s == nil || s.URL != "https://insecure.example/api/oauth/usage" {
		t.Errorf("non-https mapping = %+v", s)
	}

	// No base URL (or Anthropic's own) uses the official endpoint; the OAuth
	// token wins there.
	official := map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "oauth", "ANTHROPIC_AUTH_TOKEN": "auth"}
	for _, base := range []string{"", "https://api.anthropic.com", "https://API.anthropic.com/v1/"} {
		env := map[string]string{"ANTHROPIC_BASE_URL": base}
		for k, v := range official {
			env[k] = v
		}
		s = ResolveSource(env, nil, nil)
		if s == nil || s.URL != "https://api.anthropic.com/api/oauth/usage" || s.Token != "oauth" || s.Provider != "api.anthropic.com" ||
			s.Headers["anthropic-beta"] != "oauth-2025-04-20" {
			t.Fatalf("official (%q) = %+v", base, s)
		}
	}
	s = ResolveSource(map[string]string{"ANTHROPIC_API_KEY": "key"}, nil, nil)
	if s == nil || s.Token != "key" {
		t.Fatalf("official fallback token = %+v", s)
	}
	if ResolveSource(map[string]string{}, nil, nil) != nil {
		t.Error("no token should be unavailable")
	}
	// The user's own base URL beats the deployment's official default.
	s = ResolveSource(official, nil, []models.CustomEnvVar{{Key: "ANTHROPIC_BASE_URL", Value: "https://proxy.example.com"}})
	if s == nil || s.URL != "https://proxy.example.com/api/oauth/usage" || s.Token != "auth" {
		t.Fatalf("user proxy = %+v", s)
	}

	// The documented bitsec mapping keeps working.
	bitsec := map[string]string{"claude.bitsec.dev": "https://claude-admin.bitsec.dev/api/usage"}
	s = ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://claude.bitsec.dev", "ANTHROPIC_AUTH_TOKEN": "b", "CLAUDE_CODE_OAUTH_TOKEN": "o"}, bitsec, nil)
	if s == nil || s.URL != "https://claude-admin.bitsec.dev/api/usage" || s.Token != "b" {
		t.Fatalf("bitsec = %+v", s)
	}
}

func TestParseBody(t *testing.T) {
	var body any
	_ = json.Unmarshal([]byte(`{"five_hour":{"utilization":42.5,"resets_at":"2026-01-02T03:04:05Z"},
		"seven_day":{"usedPercent":150,"resetsAt":1767323045000}}`), &body)
	r := ParseBody("p", body)
	if r.Status != "ok" || r.Session == nil || r.Session.UsedPercent != 42.5 || r.Weekly == nil || r.Weekly.UsedPercent != 100 {
		t.Fatalf("resp = %+v", r)
	}
	if !r.Session.ResetsAt.Valid || !r.Session.ResetsAt.Time.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("session reset = %+v", r.Session.ResetsAt)
	}
	if !r.Weekly.ResetsAt.Valid || r.Weekly.ResetsAt.Time.Unix() != 1767323045 {
		t.Errorf("weekly reset = %+v", r.Weekly.ResetsAt)
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
	if r.Status != "ok" || r.Session == nil || r.Session.UsedPercent != 10 {
		t.Fatalf("fetch = %+v", r)
	}
}
