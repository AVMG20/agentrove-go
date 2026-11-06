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

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/httpx"
)

// prRepo fakes a repo's open PRs (issues and pulls listings) and comments.
type prRepo struct {
	mu       sync.Mutex
	prs      map[int]*fakePR
	comments []map[string]any
}

type fakePR struct {
	created time.Time
	sha     string
}

func (f *prRepo) open(n int, created time.Time, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs[n] = &fakePR{created: created, sha: sha}
}

func (f *prRepo) push(n int, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs[n].sha = sha
}

func (f *prRepo) comment(n int, author string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, map[string]any{
		"issue_url": "https://api.github.com/repos/o/r/issues/" + itoa(n), "user": map[string]any{"login": author},
		"body": "fixed, please look again", "created_at": time.Now().UTC().Format(time.RFC3339),
	})
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (f *prRepo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	switch r.URL.Path {
	case "/user":
		_ = json.NewEncoder(w).Encode(map[string]any{"login": "me"})
		return
	case "/repos/o/r/issues", "/repos/o/r/pulls":
		for n, pr := range f.prs {
			item := map[string]any{"number": n, "title": "PR " + itoa(n), "html_url": "https://github.com/o/r/pull/" + itoa(n),
				"user": map[string]any{"login": "alice"}, "body": "please review", "created_at": pr.created.UTC().Format(time.RFC3339)}
			if r.URL.Path == "/repos/o/r/issues" {
				item["pull_request"] = map[string]any{}
			} else {
				item["head"] = map[string]any{"sha": pr.sha}
			}
			out = append(out, item)
		}
	case "/repos/o/r/issues/comments":
		out = f.comments
	default:
		http.NotFound(w, r)
		return
	}
	if out == nil {
		out = []map[string]any{}
	}
	_ = json.NewEncoder(w).Encode(out)
}

type threadHarness struct {
	t     *testing.T
	s     *Service
	turns *[]app.ChatTurnRequest
	repo  *prRepo
	x     *Automation
	busy  map[string]bool
}

func newThreadHarness(t *testing.T, events ...string) *threadHarness {
	s, turns := newService(t)
	repo := &prRepo{prs: map[int]*fakePR{}}
	srv := httptest.NewServer(repo)
	t.Cleanup(srv.Close)
	s.githubFor = func(context.Context, string) *GitHubClient {
		c := NewGitHubClient("tok")
		c.base = srv.URL
		return c
	}
	h := &threadHarness{t: t, s: s, turns: turns, repo: repo, busy: map[string]bool{}}
	s.a.Hooks.ChatTurnActive = func(id string) bool { return h.busy[id] }
	x, err := s.Create(context.Background(), uid, &Input{WorkspaceID: sp(wsID), Name: sp("Review PRs"), Prompt: sp("Review it"),
		ModelID: sp("sonnet"), CronExpression: sp("*/5 * * * *"), Timezone: sp("UTC"), TriggerType: sp("github"),
		GithubRepo: sp("o/r"), GithubEvents: &events}, "")
	if err != nil {
		t.Fatal(err)
	}
	h.x = x
	return h
}

func (h *threadHarness) poll() {
	h.t.Helper()
	if _, err := h.s.a.DB.Exec(`UPDATE automations SET next_run_at = ? WHERE id = ?`, db.T(time.Now().Add(-time.Minute)), h.x.ID); err != nil {
		h.t.Fatal(err)
	}
	h.s.RunDue(context.Background())
}

func (h *threadHarness) wantTurns(n int) {
	h.t.Helper()
	if len(*h.turns) != n {
		h.t.Fatalf("turns = %d, want %d", len(*h.turns), n)
	}
}

func (h *threadHarness) last() app.ChatTurnRequest { return (*h.turns)[len(*h.turns)-1] }

func TestPushesContinueThePRThread(t *testing.T) {
	h := newThreadHarness(t, "pull_request_opened", "pull_request_synchronize", "issue_comment_created")
	time.Sleep(1100 * time.Millisecond) // created_at has second precision
	h.repo.open(7, time.Now(), "aaa")
	h.poll()
	h.wantTurns(1)
	thread := h.last().ChatID
	if !strings.Contains(h.last().Prompt, "type: pull_request_opened") {
		t.Fatalf("prompt:\n%s", h.last().Prompt)
	}

	// Nothing moved: no run.
	h.poll()
	h.wantTurns(1)

	// A push continues the same chat, once.
	h.repo.push(7, "bbb")
	h.poll()
	h.wantTurns(2)
	if h.last().ChatID != thread || !strings.Contains(h.last().Prompt, "type: pull_request_synchronize") ||
		!strings.Contains(h.last().Prompt, "from aaa to bbb") || !h.last().Unattended {
		t.Fatalf("push run: %+v", h.last())
	}
	h.poll()
	h.wantTurns(2)

	// Still busy with the last run: the push waits, and the next check is
	// pulled forward instead of waiting for the schedule.
	h.busy[thread] = true
	h.repo.push(7, "ccc")
	h.poll()
	h.wantTurns(2)
	x, _ := h.s.getByID(context.Background(), h.x.ID)
	if x.NextRunAt.Time.After(time.Now().Add(githubBusyRetry + 5*time.Second)) {
		t.Fatalf("no early retry: next run %v", x.NextRunAt.Time)
	}
	h.busy[thread] = false
	h.poll()
	h.wantTurns(3)
	if h.last().ChatID != thread || !strings.Contains(h.last().Prompt, "from bbb to ccc") {
		t.Fatalf("retried run: %+v", h.last())
	}

	// A comment on the PR lands in its thread too.
	time.Sleep(1100 * time.Millisecond) // past the cursor's second
	h.repo.comment(7, "alice")
	h.poll()
	h.wantTurns(4)
	if h.last().ChatID != thread || !strings.Contains(h.last().Prompt, "fixed, please look again") {
		t.Fatalf("comment run: %+v", h.last())
	}

	// An archived thread is continued (the chat runtime restores it).
	if _, err := h.s.a.DB.Exec(`UPDATE chats SET archived_at = ? WHERE id = ?`, db.Now(), thread); err != nil {
		t.Fatal(err)
	}
	h.repo.push(7, "ddd")
	h.poll()
	h.wantTurns(5)
	if h.last().ChatID != thread {
		t.Fatal("archived thread was not continued")
	}

	// A deleted thread is replaced by a new one, which later pushes continue.
	if _, err := h.s.a.DB.Exec(`UPDATE chats SET deleted_at = ? WHERE id = ?`, db.Now(), thread); err != nil {
		t.Fatal(err)
	}
	h.repo.push(7, "eee")
	h.poll()
	h.wantTurns(6)
	replacement := h.last().ChatID
	if replacement == thread {
		t.Fatal("pushed into a deleted chat")
	}
	h.repo.push(7, "fff")
	h.poll()
	h.wantTurns(7)
	if h.last().ChatID != replacement {
		t.Fatal("next push did not continue the replacement thread")
	}
}

func TestPushRunFailureIsRetried(t *testing.T) {
	h := newThreadHarness(t, "pull_request_opened", "pull_request_synchronize")
	time.Sleep(1100 * time.Millisecond)
	h.repo.open(3, time.Now(), "aaa")
	h.poll()
	h.wantTurns(1)
	h.poll() // baseline

	start := h.s.a.Hooks.StartChatTurn
	failures := 0
	h.s.a.Hooks.StartChatTurn = func(req app.ChatTurnRequest) (app.ChatTurnResult, error) {
		if failures == 0 {
			failures++
			return app.ChatTurnResult{}, httpx.Errorf(503, "sandbox down")
		}
		return start(req)
	}
	h.repo.push(3, "bbb")
	h.poll()
	h.wantTurns(1)
	h.poll()
	h.wantTurns(2)
	if !strings.Contains(h.last().Prompt, "from aaa to bbb") {
		t.Fatalf("prompt:\n%s", h.last().Prompt)
	}
}

func TestPushesToOlderPRs(t *testing.T) {
	h := newThreadHarness(t, "pull_request_opened", "pull_request_synchronize")
	// Open before the automation existed: the first look only notes its head.
	h.repo.open(1, time.Now().Add(-time.Hour), "aaa")
	h.poll()
	h.wantTurns(0)
	// Its first push starts a thread.
	h.repo.push(1, "bbb")
	h.poll()
	h.wantTurns(1)
	if !strings.Contains(h.last().Prompt, "from aaa to bbb") {
		t.Fatalf("prompt:\n%s", h.last().Prompt)
	}

	// A PR never seen before whose push arrives by webhook: the delivery's
	// "before" commit makes the push run even though the poll sees it first.
	h.repo.open(2, time.Now().Add(-time.Hour), "yyy")
	h.s.notePushBase(context.Background(), h.x.ID, 2, "xxx")
	h.poll()
	h.wantTurns(2)
	if !strings.Contains(h.last().Prompt, "from xxx to yyy") {
		t.Fatalf("prompt:\n%s", h.last().Prompt)
	}

	// Pushes made while disabled aren't replayed on re-enable.
	off, on := false, true
	ctx := context.Background()
	if _, err := h.s.Update(ctx, h.x.ID, uid, &Input{Enabled: &off, Set: map[string]bool{"enabled": true}}, ""); err != nil {
		t.Fatal(err)
	}
	h.repo.push(1, "ccc")
	if _, err := h.s.Update(ctx, h.x.ID, uid, &Input{Enabled: &on, Set: map[string]bool{"enabled": true}}, ""); err != nil {
		t.Fatal(err)
	}
	h.poll()
	h.poll()
	h.wantTurns(2)
}

func TestPROpenedAndPushTogetherRunOnce(t *testing.T) {
	h := newThreadHarness(t, "pull_request_opened", "pull_request_synchronize")
	time.Sleep(1100 * time.Millisecond)
	// Opened, then pushed before the first check; the push delivery noted
	// the PR's first commit.
	h.repo.open(4, time.Now(), "bbb")
	h.s.notePushBase(context.Background(), h.x.ID, 4, "aaa")
	h.poll()
	h.poll()
	h.wantTurns(1)
}

