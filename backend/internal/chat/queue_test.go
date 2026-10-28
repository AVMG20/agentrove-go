package chat

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/db"
)

// startRecorder stands in for startTurn: it records queued starts and lets
// go of the chat as a launched turn that finished at once would.
type startRecorder struct {
	t       *testing.T
	m       *Module
	mu      sync.Mutex
	prompts []string
}

func recordStarts(t *testing.T, m *Module) *startRecorder {
	r := &startRecorder{t: t, m: m}
	m.runTurn = func(_ context.Context, in startInput) (app.ChatTurnResult, error) {
		if !in.reserved {
			t.Error("queued start without the chat reserved")
		}
		r.mu.Lock()
		r.prompts = append(r.prompts, in.prompt)
		r.mu.Unlock()
		m.unreserve(in.chatID)
		return app.ChatTurnResult{}, nil
	}
	return r
}

func (r *startRecorder) started() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

func queued(content string) *QueuedMessage {
	return &QueuedMessage{ID: content, Content: content, ModelID: "sonnet", PermissionMode: "bypassPermissions",
		SelectedPersonaName: DefaultPersona, QueuedAt: db.Now()}
}

func queueLen(t *testing.T, m *Module) int {
	q, err := m.st.listQueue(context.Background(), testChat)
	if err != nil {
		t.Fatal(err)
	}
	return len(q)
}

// The turn already found the queue empty in finish but still holds the chat
// when the message arrives: enqueue sees it busy, so release must start it.
func TestMessageQueuedAsTurnEndsIsStarted(t *testing.T) {
	m, _ := newTestModule(t)
	rec := recordStarts(t, m)
	tr := newTestTurn(m)
	tr.queueMode = queueAll
	m.turns[testChat] = tr

	if _, err := m.enqueue(context.Background(), testChat, testUser, queued("late")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := rec.started(); len(got) != 0 {
		t.Fatalf("started while the turn held the chat: %v", got)
	}
	m.release(tr)
	if got := rec.started(); fmt.Sprint(got) != "[late]" || queueLen(t, m) != 0 {
		t.Fatalf("started %v, %d left queued", got, queueLen(t, m))
	}
}

func TestMessageQueuedOnIdleChatIsStarted(t *testing.T) {
	m, _ := newTestModule(t)
	rec := recordStarts(t, m)
	if _, err := m.enqueue(context.Background(), testChat, testUser, queued("idle")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, "start", func() bool { return len(rec.started()) == 1 })
}

// Failed turns keep their queue (the user decides); interrupted ones only run
// send-now messages.
func TestReleaseHonoursQueueMode(t *testing.T) {
	for _, mode := range []string{"", queueSendNow} {
		m, _ := newTestModule(t)
		rec := recordStarts(t, m)
		tr := newTestTurn(m)
		tr.queueMode = mode
		m.turns[testChat] = tr
		if _, err := m.enqueue(context.Background(), testChat, testUser, queued("next")); err != nil {
			t.Fatal(err)
		}
		m.release(tr)
		if got := rec.started(); len(got) != 0 || queueLen(t, m) != 1 {
			t.Fatalf("mode %q: started %v, %d queued", mode, got, queueLen(t, m))
		}
	}
}

// Enqueue racing the turn letting go: every message starts exactly once.
func TestEnqueueRacingReleaseNeverStrands(t *testing.T) {
	m, _ := newTestModule(t)
	rec := recordStarts(t, m)
	const n = 200
	for i := range n {
		tr := newTestTurn(m)
		tr.queueMode = queueAll
		m.mu.Lock()
		m.turns[testChat] = tr
		m.mu.Unlock()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := m.enqueue(context.Background(), testChat, testUser, queued(fmt.Sprint(i))); err != nil {
				t.Error(err)
			}
		}()
		go func() { defer wg.Done(); m.release(tr) }()
		wg.Wait()
		waitFor(t, 2*time.Second, fmt.Sprintf("message %d", i), func() bool { return len(rec.started()) == i+1 })
	}
	time.Sleep(50 * time.Millisecond)
	seen := map[string]int{}
	for _, p := range rec.started() {
		seen[p]++
	}
	if len(seen) != n || queueLen(t, m) != 0 {
		t.Fatalf("%d distinct starts of %d, %d left queued", len(seen), n, queueLen(t, m))
	}
	for p, c := range seen {
		if c != 1 {
			t.Fatalf("message %s started %d times", p, c)
		}
	}
}
