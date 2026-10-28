package chat

import (
	"context"
	"testing"
)

// A result that comes before the CLI echoes a steered message belongs to
// the cycle running ahead of it and must not end the turn.
func TestCycleWaitsForSteeredEchoes(t *testing.T) {
	c := newCycle("own")
	if c.settled() {
		t.Fatal("settled before the turn's own prompt was echoed")
	}
	if q := c.replayed("own"); q != nil {
		t.Fatal("own echo returned a steered message")
	}
	if !c.settled() {
		t.Fatal("not settled after the own echo")
	}

	steered := queued("steer")
	c.steered["s1"] = steered
	if c.settled() {
		t.Fatal("settled with a steered message not yet echoed")
	}
	if got := c.unechoed(); len(got) != 1 || got["s1"] != steered {
		t.Fatalf("unechoed = %v, want s1", got)
	}
	if q := c.replayed("s1"); q != steered {
		t.Fatalf("replayed(s1) = %v, want the steered message", q)
	}
	if !c.settled() {
		t.Fatal("not settled after every echo")
	}
}

// A follow-up turn sends no prompt of its own; only steers hold it open.
func TestCycleWithoutOwnPrompt(t *testing.T) {
	c := newCycle("")
	if !c.settled() {
		t.Fatal("follow-up cycle not settled")
	}
}

func TestSteeringOnlyAcceptsWhileOpen(t *testing.T) {
	s := newSteering()
	if s.offer(queued("early")) {
		t.Fatal("accepted a message before the turn's loop started")
	}
	s.start()
	if !s.offer(queued("a")) || !s.offer(queued("b")) {
		t.Fatal("rejected a message while open")
	}
	select {
	case <-s.ready:
	default:
		t.Fatal("offer did not signal ready")
	}
	if got := s.take(); len(got) != 2 {
		t.Fatalf("take = %d messages, want 2", len(got))
	}
	s.offer(queued("c"))
	if left := s.close(); len(left) != 1 || left[0].Content != "c" {
		t.Fatalf("close returned %v, want the untaken message", left)
	}
	if s.offer(queued("late")) {
		t.Fatal("accepted a message after close")
	}
}

// Send now on a running Claude turn steers: the message leaves the queue for
// the turn, and the turn (with its subagents) keeps running.
func TestSendNowSteersRunningTurn(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	if err := m.st.enqueue(ctx, testChat, queued("steer me")); err != nil {
		t.Fatal(err)
	}
	tr := newTestTurn(m)
	tr.steer.start()

	if err := m.sendNowRunning(ctx, tr, testChat, "steer me"); err != nil {
		t.Fatal(err)
	}
	if tr.cancelled.Load() || tr.ctx.Err() != nil {
		t.Fatal("steering interrupted the turn")
	}
	if got := tr.steer.take(); len(got) != 1 || got[0].Content != "steer me" {
		t.Fatalf("turn got %v, want the steered message", got)
	}
	if n := queueLen(t, m); n != 0 {
		t.Fatalf("queue has %d messages, want 0", n)
	}
}

// A turn that can't steer (another provider, or past its loop) is interrupted
// and the message stays queued as send-now to take over the stream.
func TestSendNowInterruptsTurnThatCannotSteer(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	q := queued("run me")
	if err := m.st.enqueue(ctx, testChat, q); err != nil {
		t.Fatal(err)
	}
	if _, err := m.st.db.ExecContext(ctx, `UPDATE queued_messages SET send_now = 1 WHERE id = ?`, q.ID); err != nil {
		t.Fatal(err)
	}
	tr := newTestTurn(m)

	if err := m.sendNowRunning(ctx, tr, testChat, "run me"); err != nil {
		t.Fatal(err)
	}
	if !tr.cancelled.Load() || tr.ctx.Err() == nil {
		t.Fatal("turn not interrupted")
	}
	if !m.st.hasQueued(ctx, testChat, true) {
		t.Fatal("message no longer queued as send-now")
	}
}

// A stopped turn puts a steered message it never sent back in the queue for
// the user, not flagged to run.
func TestStoppedTurnRequeuesSteerWithoutRunningIt(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	tr := newTestTurn(m)
	tr.requeueSteered(queued("later"), false)
	if !m.st.hasQueued(ctx, testChat, false) {
		t.Fatal("message not back in the queue")
	}
	if m.st.hasQueued(ctx, testChat, true) {
		t.Fatal("stopped steer would run on its own")
	}
}
