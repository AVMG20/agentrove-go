package usage

import (
	"encoding/json"
	"testing"
	"time"

	"agentrove/internal/models"
)

func TestResolveSource(t *testing.T) {
	endpoints := map[string]string{"Proxy.Example.com": "https://proxy.example.com/usage", "insecure.example": "http://x"}
	global := map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com/v1", "ANTHROPIC_API_KEY": "k1"}
	s := ResolveSource(global, endpoints, nil)
	if s == nil || s.Provider != "proxy.example.com" || s.Token != "k1" || s.URL != "https://proxy.example.com/usage" {
		t.Fatalf("source = %+v", s)
	}
	// User vars override the deployment env; AUTH_TOKEN wins over API_KEY.
	s = ResolveSource(global, endpoints, []models.CustomEnvVar{{Key: "ANTHROPIC_AUTH_TOKEN", Value: "t2"}})
	if s == nil || s.Token != "t2" {
		t.Fatalf("override = %+v", s)
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://other.example", "ANTHROPIC_API_KEY": "k"}, endpoints, nil) != nil {
		t.Error("unknown host should be unavailable")
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://insecure.example", "ANTHROPIC_API_KEY": "k"}, endpoints, nil) != nil {
		t.Error("non-https endpoint must be ignored")
	}
	if ResolveSource(map[string]string{"ANTHROPIC_BASE_URL": "https://proxy.example.com"}, endpoints, nil) != nil {
		t.Error("missing token should be unavailable")
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
