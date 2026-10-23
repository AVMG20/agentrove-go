package automation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"agentrove/internal/db"
)

// fakeGitHub serves /user and /repos/o/r/issues from a mutable list.
type fakeGitHub struct {
	mu     sync.Mutex
	issues []map[string]any
}

func (f *fakeGitHub) add(n int, author string, pr bool, created time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item := map[string]any{"number": n, "title": "Item " + time.Now().String(), "html_url": "https://github.com/o/r/pull/1",
		"user": map[string]any{"login": author}, "body": "please review", "created_at": created.UTC().Format(time.RFC3339)}
	if pr {
		item["pull_request"] = map[string]any{}
	}
	f.issues = append([]map[string]any{item}, f.issues...)
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.URL.Path {
	case "/user":
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "me"})
	case "/repos/o/r/issues":
		_ = json.NewEncoder(w).Encode(f.issues)
	case "/repos/o/r/issues/comments":
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"issue_url": "https://api.github.com/repos/o/r/issues/2", "user": map[string]any{"login": "me"}, "body": "LGTM, merging", "created_at": time.Now().UTC().Format(time.RFC3339)},
		})
	default:
		http.NotFound(w, r)
	}
}

func TestGitHubPollFiresEachNewPROnce(t *testing.T) {
	s, turns := newService(t)
	gh := &fakeGitHub{}
	srv := httptest.NewServer(gh)
	defer srv.Close()
	s.githubFor = func(context.Context, string) *GitHubClient {
		c := NewGitHubClient("tok")
		c.base = srv.URL
		return c
	}
	ctx := context.Background()
	x, err := s.Create(ctx, uid, &Input{WorkspaceID: sp(wsID), Name: sp("Review PRs"), Prompt: sp("Review and merge if green"),
		ModelID: sp("sonnet"), CronExpression: sp("*/5 * * * *"), Timezone: sp("UTC"), TriggerType: sp("github"),
		GithubRepo: sp("o/r"), GithubEvents: &[]string{"pull_request_opened", "issue_comment_created"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	due := func() {
		if _, err := s.a.DB.Exec(`UPDATE automations SET next_run_at = ? WHERE id = ?`, db.T(time.Now().Add(-time.Minute)), x.ID); err != nil {
			t.Fatal(err)
		}
		s.RunDue(ctx)
	}

	// An old PR exists before the automation watches: the first poll only
	// sets the cursor.
	gh.add(1, "alice", true, time.Now().Add(-time.Hour))
	due()
	if len(*turns) != 0 {
		t.Fatalf("replayed history: %d turns", len(*turns))
	}

	// New activity: PRs fire (ours too: Claude opens PRs with our token), an
	// issue doesn't (not watched), and neither does our own comment.
	time.Sleep(1100 * time.Millisecond) // created_at has second precision
	gh.add(2, "bob", true, time.Now())
	gh.add(3, "me", true, time.Now())
	gh.add(4, "carol", false, time.Now())
	due()
	if len(*turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(*turns))
	}
	byNumber := map[string]int{}
	for i, tr := range *turns {
		for _, n := range []string{"#2", "#3"} {
			if strings.Contains(tr.Prompt, "number: "+n+"\n") {
				byNumber[n] = i
			}
		}
	}
	if len(byNumber) != 2 {
		t.Fatalf("want runs for PR #2 and #3, got %+v", *turns)
	}
	tr := (*turns)[byNumber["#2"]]
	if !tr.Unattended || !strings.Contains(tr.Prompt, "Review and merge if green") || !strings.Contains(tr.Prompt, "author: bob") {
		t.Fatalf("prompt:\n%s", tr.Prompt)
	}
	if !strings.Contains((*turns)[byNumber["#3"]].Prompt, "author: me") {
		t.Fatal("own PR should fire")
	}
	chat, err := s.a.Store.GetChat(ctx, uid, tr.ChatID)
	if err != nil || chat.Title != "Review PRs · PR #2" || chat.AutomationID == nil || *chat.AutomationID != x.ID {
		t.Fatalf("chat: %+v %v", chat, err)
	}

	// The next poll doesn't fire them again.
	due()
	if len(*turns) != 2 {
		t.Fatalf("fired twice: %d turns", len(*turns))
	}
}
