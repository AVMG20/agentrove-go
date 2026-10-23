package channel

import (
	"reflect"
	"strings"
	"testing"
)

func testMembers() []*Member {
	return []*Member{
		{ID: "m1", DisplayName: "opus"},
		{ID: "m2", DisplayName: "Sonnet-5.5"},
		{ID: "m3", DisplayName: "haiku", Introduced: true},
	}
}

func TestMentions(t *testing.T) {
	cases := []struct {
		name, text, author string
		want               []string
		all                bool
	}{
		{name: "none", text: "hello there", want: []string{}},
		{name: "one", text: "@opus what do you think?", want: []string{"m1"}},
		{name: "case and punctuation", text: "thoughts, @SONNET-5.5?", want: []string{"m2"}},
		{name: "trailing dot kept when part of name", text: "ask @sonnet-5.5.", want: []string{"m2"}},
		{name: "several sorted", text: "@haiku and @opus: go", want: []string{"m1", "m3"}},
		{name: "email is not a mention", text: "mail me at bob@opus.dev", want: []string{}},
		{name: "author excluded", text: "@opus @haiku", author: "m1", want: []string{"m3"}},
		{name: "broadcast", text: "@everyone please review", all: true},
		{name: "broadcast with punctuation", text: "hey @all!", all: true},
		{name: "unknown name", text: "@gpt answer", want: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, all := mentions(testMembers(), c.text, c.author)
			if all != c.all {
				t.Fatalf("all = %v, want %v", all, c.all)
			}
			if !c.all && !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestUserAudience(t *testing.T) {
	if a := userAudience(testMembers(), "hi all"); a != nil {
		t.Fatalf("no mention should broadcast, got %v", a)
	}
	if a := userAudience(testMembers(), "@all hi"); a != nil {
		t.Fatalf("@all should broadcast, got %v", a)
	}
	if a := userAudience(testMembers(), "@opus hi"); !reflect.DeepEqual(a, []string{"m1"}) {
		t.Fatalf("got %v", a)
	}
}

func strp(s string) *string { return &s }

func TestReplyAudience(t *testing.T) {
	members := testMembers()
	me := members[0]
	cases := []struct {
		name  string
		text  string
		batch []*Message
		want  []string // nil = broadcast
	}{
		{name: "explicit mention wins", text: "@haiku can you check", batch: []*Message{{Audience: nil}}, want: []string{"m3"}},
		{name: "explicit broadcast", text: "@everyone done", batch: []*Message{{Audience: []string{"m1"}}}, want: nil},
		{name: "reply to broadcast broadcasts", text: "ok", batch: []*Message{{Audience: nil}}, want: nil},
		{name: "reply to user-only targeted message", text: "sure", batch: []*Message{{Audience: []string{"m1"}}}, want: []string{}},
		{name: "reply to member", text: "agreed", batch: []*Message{{MemberID: strp("m2"), Audience: []string{"m1", "m3"}}}, want: []string{"m2", "m3"}},
		{name: "skip messages not addressed to me", text: "x", batch: []*Message{{MemberID: strp("m2"), Audience: []string{"m3"}}}, want: []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := replyAudience(members, me, c.text, c.batch)
			if c.want == nil {
				if got != nil {
					t.Fatalf("want broadcast (nil), got %v", got)
				}
				return
			}
			if got == nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %#v, want %#v", got, c.want)
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	members := testMembers()
	batch := []*Message{
		{Content: "kick off"},
		{MemberID: strp("m2"), Content: "I think X", Audience: []string{"m1"}},
		{MemberID: strp("m3"), Content: "to the user", Audience: []string{}},
	}
	got := buildPrompt(members, members[0], batch)
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "You are opus, a member of a group chat channel with the user and Sonnet-5.5, haiku.") {
		t.Fatalf("intro line: %q", lines[0])
	}
	if lines[1] != routingNote {
		t.Fatalf("routing note missing: %q", lines[1])
	}
	want := []string{"[user]: kick off", "[Sonnet-5.5 → @opus]: I think X", "[haiku → user]: to the user"}
	if !reflect.DeepEqual(lines[2:], want) {
		t.Fatalf("lines = %q", lines[2:])
	}

	// Introduced member, broadcast-only batch: just the lines.
	got = buildPrompt(members, members[2], []*Message{{Content: "hello"}})
	if got != "[user]: hello" {
		t.Fatalf("got %q", got)
	}

	solo := []*Member{{ID: "x", DisplayName: "solo"}}
	if got := buildPrompt(solo, solo[0], nil); !strings.Contains(got, "with the user and no other agents.") {
		t.Fatalf("solo intro: %q", got)
	}
}

func TestIsSilent(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"", true},
		{"   ", true},
		{"PASS", true},
		{"pass", true},
		{"`PASS`", true},
		{"**PASS**.", true},
		{"P", true},   // streaming prefix
		{"PA", true},  // streaming prefix
		{"Pa", true},  // prefix check is case-insensitive
		{"Pas", true}, // streaming prefix
		{"Passing thought: we should refactor", false},
		{"PASS — nothing to add", true},
		{"PASS: covered already", true},
		{"PASS - nothing new", true},
		{"pass, nothing to add", true},
		{"Good point.\nPASS", true},
		{"I agree with that. PASS", true},
		{"Nothing to add; PASS.", true},
		{"Let's run the tests first.", false},
		{"Use the PASS keyword in the config", false},
		// Code-only replies are content, not a pass (matches the Python port).
		{"```\nPASS\n```", false},
		{"```go\nfunc main() {}\n```\nLooks good", false},
		{"```\ncode\n```", false},
		{"    indented code only", false},
		{"\tPASS", false},
		{"Here is the fix:\n    x := 1\nApplied.", false},
		{"~~~\nstuff\n~~~\nPASS", true},
		{"```\nfoo\n````  \nPASS", true},
		{"  ```\nx\n   ```\nok PASS", false},
	}
	for _, c := range cases {
		if got := isSilent(c.text); got != c.want {
			t.Errorf("isSilent(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestDefaultDisplayName(t *testing.T) {
	used := map[string]bool{"all": true, "everyone": true}
	cases := []struct{ model, want string }{
		{"Opus 5.5", "opus-5.5"},
		{"Opus 5.5", "opus-5.5-2"},
		{"Opus 5.5", "opus-5.5-3"},
		{"Sonnet", "sonnet"},
		{"!!!", "claude"},
		{"A Very Long Model Name That Exceeds The Limit", "a-very-long-model-name-that-exce"},
		{"A Very Long Model Name That Exceeds The Limit", "a-very-long-model-name-that-ex-2"},
	}
	for _, c := range cases {
		got := defaultDisplayName(c.model, used)
		if got != c.want {
			t.Errorf("defaultDisplayName(%q) = %q, want %q", c.model, got, c.want)
		}
		used[strings.ToLower(got)] = true
	}
}

func TestValidateMembers(t *testing.T) {
	cases := []struct {
		name    string
		members []MemberInput
		want    []string
		wantErr string
	}{
		{name: "defaults", members: []MemberInput{{ModelID: "opus"}, {ModelID: "opus"}}, want: []string{"opus", "opus-2"}},
		{name: "explicit", members: []MemberInput{{ModelID: "sonnet", DisplayName: strp(" critic ")}}, want: []string{"critic"}},
		{name: "unknown model", members: []MemberInput{{ModelID: "gpt-5"}}, wantErr: "Unknown model_id"},
		{name: "reserved", members: []MemberInput{{ModelID: "opus", DisplayName: strp("All")}}, wantErr: "reserved"},
		{name: "duplicate", members: []MemberInput{{ModelID: "opus", DisplayName: strp("x")}, {ModelID: "haiku", DisplayName: strp("X")}}, wantErr: "Duplicate display name"},
		{name: "bad chars", members: []MemberInput{{ModelID: "opus", DisplayName: strp("-bad")}}, wantErr: "Display name must"},
		{name: "bad permission", members: []MemberInput{{ModelID: "opus", PermissionMode: strp("yolo")}}, wantErr: "Invalid permission mode"},
		{name: "empty", members: nil, wantErr: "at least 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := validateMembers(c.members)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want %q", err, c.wantErr)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v, %v; want %v", got, err, c.want)
			}
		})
	}
}

func TestNormalizeBranch(t *testing.T) {
	cases := []struct {
		in      *string
		want    string
		wantErr bool
	}{
		{in: nil}, {in: strp("  ")}, {in: strp(" main "), want: "main"}, {in: strp("feature/x-1"), want: "feature/x-1"},
		{in: strp("a..b"), wantErr: true}, {in: strp("-x"), wantErr: true}, {in: strp("x.lock"), wantErr: true}, {in: strp("x/"), wantErr: true},
	}
	for _, c := range cases {
		got, err := normalizeBranch(c.in)
		if c.wantErr != (err != nil) {
			t.Errorf("normalizeBranch(%v) err = %v", c.in, err)
			continue
		}
		if db := derefStr(got); db != c.want {
			t.Errorf("normalizeBranch(%v) = %q, want %q", c.in, db, c.want)
		}
	}
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
