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
	s = ResolveSource(map[string]string{"ANTHROPIC_API_KEY": "key"}, nil)
	if s == nil || s.Token != "key" {
		t.Fatalf("official fallback token = %+v", s)
	}
	if ResolveSource(map[string]string{}, nil) != nil {
		t.Error("no token should be unavailable")
	}
	// The user's own base URL beats the deployment's official default.
	s = ResolveSource(official, []models.CustomEnvVar{{Key: "ANTHROPIC_BASE_URL", Value: "https://proxy.example.com"}})
	if s == nil || s.URL != "https://proxy.example.com/api/usage" || s.Token != "auth" {
		t.Fatalf("user proxy = %+v", s)
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
