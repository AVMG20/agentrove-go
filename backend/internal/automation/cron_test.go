package automation

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNextRun(t *testing.T) {
	cases := []struct {
		name, expr, tz, base, want string
		wantErr                    bool
	}{
		{name: "daily utc", expr: "0 9 * * *", tz: "UTC", base: "2026-03-01T08:00:00Z", want: "2026-03-01T09:00:00Z"},
		{name: "daily utc after slot", expr: "0 9 * * *", tz: "UTC", base: "2026-03-01T09:00:00Z", want: "2026-03-02T09:00:00Z"},
		{name: "new york winter", expr: "0 9 * * *", tz: "America/New_York", base: "2026-01-15T12:00:00Z", want: "2026-01-15T14:00:00Z"},
		{name: "new york summer", expr: "0 9 * * *", tz: "America/New_York", base: "2026-07-15T12:00:00Z", want: "2026-07-15T13:00:00Z"},
		{name: "amsterdam weekly monday", expr: "30 7 * * 1", tz: "Europe/Amsterdam", base: "2026-10-21T00:00:00Z", want: "2026-10-26T06:30:00Z"},
		{name: "every 6 hours", expr: "0 */6 * * *", tz: "UTC", base: "2026-03-01T07:15:00Z", want: "2026-03-01T12:00:00Z"},
		{name: "monthly", expr: "0 0 15 * *", tz: "Asia/Tokyo", base: "2026-02-20T00:00:00Z", want: "2026-03-14T15:00:00Z"},
		// Spring forward: 02:30 doesn't exist on 2026-03-08 in New York; the
		// slot is skipped that day.
		{name: "spring forward gap", expr: "30 2 * * *", tz: "America/New_York", base: "2026-03-08T05:00:00Z", want: "2026-03-09T06:30:00Z"},
		// Fall back: 01:30 occurs twice on 2026-11-01; fire it once.
		{name: "fall back fires once", expr: "30 1 * * *", tz: "America/New_York", base: "2026-11-01T05:31:00Z", want: "2026-11-02T06:30:00Z"},
		{name: "descriptor", expr: "@daily", tz: "UTC", base: "2026-03-01T08:00:00Z", want: "2026-03-02T00:00:00Z"},
		{name: "no future", expr: "0 0 30 2 *", tz: "UTC", base: "2026-03-01T08:00:00Z", wantErr: true},
		{name: "bad tz", expr: "0 9 * * *", tz: "Mars/Olympus", base: "2026-03-01T08:00:00Z", wantErr: true},
		{name: "bad expr", expr: "61 * * * *", tz: "UTC", base: "2026-03-01T08:00:00Z", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NextRun(c.expr, c.tz, mustTime(t, c.base))
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := mustTime(t, c.want); !got.Equal(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			if got.Location() != time.UTC {
				t.Fatalf("result not UTC: %v", got.Location())
			}
		})
	}
}

func TestValidCron(t *testing.T) {
	for expr, want := range map[string]bool{
		"* * * * *": true, "0 9 * * 1-5": true, "@hourly": true, "0 9 * *": false, "every day": false, "": false,
	} {
		if got := ValidCron(expr); got != want {
			t.Errorf("ValidCron(%q) = %v, want %v", expr, got, want)
		}
	}
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyGitHubSignature(t *testing.T) {
	body := `{"action":"opened"}`
	cases := []struct {
		name, secret, body, sig string
		want                    bool
	}{
		{"valid", "s3cret", body, sign("s3cret", body), true},
		{"wrong secret", "s3cret", body, sign("other", body), false},
		{"tampered body", "s3cret", body + " ", sign("s3cret", body), false},
		{"missing", "s3cret", body, "", false},
		{"sha1 prefix", "s3cret", body, strings.Replace(sign("s3cret", body), "sha256=", "sha1=", 1), false},
		{"uppercase hex", "s3cret", body, "sha256=" + strings.ToUpper(strings.TrimPrefix(sign("s3cret", body), "sha256=")), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := VerifyGitHubSignature(c.secret, []byte(c.body), c.sig); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestNormalizeGitHubRepo(t *testing.T) {
	cases := []struct{ in, want string }{
		{"octo/repo", "octo/repo"},
		{" https://github.com/octo/repo.js/ ", "octo/repo.js"},
		{"octo", ""},
		{"octo/repo/extra", ""},
		{"oc to/repo", ""},
	}
	for _, c := range cases {
		got, err := NormalizeGitHubRepo(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("%q: want error, got %q", c.in, got)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q: got %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestNormalizeGitHubEvents(t *testing.T) {
	got, err := NormalizeGitHubEvents([]string{"issue_opened", "pull_request_opened", "issue_opened"})
	if err != nil || strings.Join(got, ",") != "issue_opened,pull_request_opened" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := NormalizeGitHubEvents([]string{"push"}); err == nil {
		t.Fatal("want error for unknown event")
	}
}

func TestEventPromptAndTitle(t *testing.T) {
	n := 42
	ev := &ActivityEvent{Event: "issue_opened", Repo: "octo/repo", Number: &n, Title: "Bug", URL: "https://x", Author: "alice", Body: "  "}
	p := EventPrompt("Triage it", ev)
	for _, want := range []string{"Triage it\n\n<github_event>", "number: #42", "body:\n(empty)\n</github_event>", "gh issue comment 42 --repo octo/repo"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
	x := &Automation{Name: "Triage", Timezone: "UTC"}
	if got := RunTitle(x, ev, time.Now()); got != "Triage · Issue #42" {
		t.Errorf("title = %q", got)
	}
	if got := RunTitle(x, nil, mustTime(t, "2026-03-05T14:07:00Z")); got != "Triage · Mar 05, 14:07" {
		t.Errorf("title = %q", got)
	}
	rel := &ActivityEvent{Event: "release_published", Title: "v1.0"}
	if got := RunTitle(x, rel, time.Now()); got != "Triage · v1.0" {
		t.Errorf("title = %q", got)
	}
}
