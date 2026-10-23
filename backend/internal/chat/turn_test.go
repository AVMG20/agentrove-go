package chat

import (
	"context"
	"fmt"
	"testing"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/models"
)

const (
	testUser = "11111111-1111-1111-1111-111111111111"
	testWS   = "22222222-2222-2222-2222-222222222222"
	testChat = "33333333-3333-3333-3333-333333333333"
	testMsg  = "44444444-4444-4444-4444-444444444444"
)

// newTestModule returns a Module backed by a temp DB holding one chat with
// an in-progress assistant message. It has no App: tests stay off paths
// that need sandboxes or the hub.
func newTestModule(t *testing.T) (*Module, *db.DB) {
	t.Helper()
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := db.Now()
	for _, q := range []string{
		`INSERT INTO users (id, email, username, hashed_password, created_at, updated_at) VALUES ('` + testUser + `', 'a@b.c', 'a', 'x', ?, ?)`,
		`INSERT INTO workspaces (id, name, user_id, sandbox_id, workspace_path, created_at, updated_at) VALUES ('` + testWS + `', 'ws', '` + testUser + `', 'sb', '/tmp', ?, ?)`,
		`INSERT INTO chats (id, title, user_id, workspace_id, created_at, updated_at) VALUES ('` + testChat + `', 'c', '` + testUser + `', '` + testWS + `', ?, ?)`,
		`INSERT INTO messages (id, chat_id, role, stream_status, created_at, updated_at) VALUES ('` + testMsg + `', '` + testChat + `', 'assistant', 'in_progress', ?, ?)`,
	} {
		if _, err := d.Exec(q, now, now); err != nil {
			t.Fatal(err)
		}
	}
	m := &Module{
		st: &store{db: d}, sessions: newSessions(),
		turns: map[string]*turn{}, attached: map[string]*turn{}, starting: map[string]bool{},
		bgTasks: map[string][]backgroundTask{}, bgOwner: map[string]string{}, pending: map[string][]claude.Message{},
	}
	return m, d
}

// newTestTurn builds a channel-member turn so emits never publish to the hub.
func newTestTurn(m *Module) *turn {
	return newTurn(m, &turnRequest{
		chat: &models.Chat{ID: testChat}, userID: testUser, messageID: testMsg,
		member: func(string, map[string]any) {},
	})
}

func storedKinds(t *testing.T, d *db.DB) []string {
	t.Helper()
	rows, err := d.Query(`SELECT seq, event_type FROM message_events WHERE message_id = ? ORDER BY seq`, testMsg)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int64
		var kind string
		if err := rows.Scan(&seq, &kind); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d:%s", seq, kind))
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func bufLen(tr *turn) int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return len(tr.buf)
}

// A tool_started right before a long tool run must reach clients without
// waiting for the next event.
func TestFlushTimerSendsLoneBufferedEvent(t *testing.T) {
	m, d := newTestModule(t)
	tr := newTestTurn(m)
	tr.emit("tool_started", map[string]any{"tool": map[string]any{"id": "toolu_1", "name": "Bash"}})
	if n := bufLen(tr); n != 1 {
		t.Fatalf("buffered = %d, want the event held for batching", n)
	}
	waitFor(t, 2*time.Second, "timed flush", func() bool { return len(storedKinds(t, d)) == 1 })

	tr.mu.Lock()
	tr.closeLocked()
	armed := tr.flushTimer != nil
	tr.mu.Unlock()
	if armed {
		t.Fatal("flush timer still armed after close")
	}
	// After close, snapshot kinds flush inline: no timer would send them.
	tr.emit("assistant_text", map[string]any{"text": "late"})
	if got := storedKinds(t, d); len(got) != 2 {
		t.Fatalf("events after close = %v", got)
	}
}

func failEventWrites(t *testing.T, d *db.DB) func() {
	t.Helper()
	if _, err := d.Exec(`CREATE TRIGGER fail_events BEFORE INSERT ON message_events BEGIN SELECT RAISE(ABORT, 'disk on fire'); END`); err != nil {
		t.Fatal(err)
	}
	return func() {
		if _, err := d.Exec(`DROP TRIGGER fail_events`); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFailedEventWriteIsRetriedInOrder(t *testing.T) {
	m, d := newTestModule(t)
	tr := newTestTurn(m)
	heal := failEventWrites(t, d)

	tr.emit("stream_started", map[string]any{"status": "started"})
	tr.emit("assistant_text", map[string]any{"text": "a"})
	tr.emit("permission_request", map[string]any{"request_id": "r1"})
	if got := storedKinds(t, d); len(got) != 0 {
		t.Fatalf("stored while failing: %v", got)
	}
	if n := bufLen(tr); n != 3 {
		t.Fatalf("buffered = %d, want 3 kept for retry", n)
	}
	heal()
	waitFor(t, 3*time.Second, "retry", func() bool { return len(storedKinds(t, d)) == 3 })
	want := []string{"1:stream_started", "2:assistant_text", "3:permission_request"}
	if got := storedKinds(t, d); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	var seq int64
	_ = d.QueryRow(`SELECT last_event_seq FROM chats WHERE id = ?`, testChat).Scan(&seq)
	if seq != 3 {
		t.Fatalf("last_event_seq = %d, want 3 (failed writes must not burn seqs)", seq)
	}
}

func TestRetryBufferDropsOldestBeyondCap(t *testing.T) {
	m, d := newTestModule(t)
	tr := newTestTurn(m)
	heal := failEventWrites(t, d)
	extra := 10
	for i := range maxBufferedEvents + extra {
		tr.emit("assistant_text", map[string]any{"text": fmt.Sprint(i)})
	}
	// The next (timed) retry fails too and trims the buffer to the cap.
	waitFor(t, 3*time.Second, "trim", func() bool { return bufLen(tr) == maxBufferedEvents })
	heal()
	waitFor(t, 3*time.Second, "retry", func() bool { return bufLen(tr) == 0 })
	var n int
	var first string
	_ = d.QueryRow(`SELECT COUNT(1) FROM message_events WHERE message_id = ?`, testMsg).Scan(&n)
	_ = d.QueryRow(`SELECT json_extract(render_payload, '$.text') FROM message_events WHERE message_id = ? ORDER BY seq LIMIT 1`, testMsg).Scan(&first)
	if n != maxBufferedEvents || first != fmt.Sprint(extra) {
		t.Fatalf("stored %d events starting at %q, want %d starting at %q", n, first, maxBufferedEvents, fmt.Sprint(extra))
	}
	// The render snapshot still has every event.
	tr.mu.Lock()
	text := tr.contentText()
	tr.mu.Unlock()
	if want := maxBufferedEvents + extra; len(text) < want {
		t.Fatalf("snapshot text lost events: %d chars", len(text))
	}
}

func TestCheckpointDue(t *testing.T) {
	tr := &turn{}
	old := time.Now().Add(-time.Hour)
	cases := []struct {
		name string
		set  func()
		want bool
	}{
		{"unchanged render", func() { tr.lastCheckpoint = old }, false},
		{"changed render", func() { tr.renderVersion++ }, true},
		{"large render, recent", func() { tr.checkpointBytes = 10 << 20; tr.lastCheckpoint = time.Now().Add(-10 * time.Second) }, false},
		{"large render, past max interval", func() { tr.lastCheckpoint = time.Now().Add(-checkpointMaxInterval) }, true},
		{"unchanged render, replay lag", func() {
			tr.checkpointVersion = tr.renderVersion
			tr.lastCheckpoint = time.Now()
			tr.lastSeq = tr.checkpointSeq + checkpointMaxLag
		}, true},
	}
	for _, c := range cases {
		c.set()
		if got := tr.checkpointDueLocked(); got != c.want {
			t.Fatalf("%s: due = %v, want %v", c.name, got, c.want)
		}
	}
}

// Mid-turn checkpoints plus the event log after them must rebuild the turn
// after a crash (reconcileOrphans → finalizeDead).
func TestSkippedCheckpointsStillRecover(t *testing.T) {
	m, d := newTestModule(t)
	tr := newTestTurn(m)
	tr.emit("stream_started", map[string]any{})
	tr.mu.Lock()
	tr.checkpointLocked(StatusInProgress)
	tr.mu.Unlock()
	for _, s := range []string{"Hello", " ", "world"} {
		tr.emit("assistant_text", map[string]any{"text": s})
	}
	tr.mu.Lock()
	tr.flushLocked()
	tr.closeLocked()
	tr.mu.Unlock()

	m.finalizeDead(context.Background(), testMsg, StatusInterrupted, "", "")
	var content, status string
	if err := d.QueryRow(`SELECT content_text, stream_status FROM messages WHERE id = ?`, testMsg).Scan(&content, &status); err != nil {
		t.Fatal(err)
	}
	if content != "Hello world" || status != StatusInterrupted {
		t.Fatalf("recovered %q (%s)", content, status)
	}
}
