package chat

import (
	"context"
	"testing"
	"time"

	"agentrove/internal/db"
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
	if s.offer(queued("early"), false) {
		t.Fatal("accepted a message before the turn's loop started")
	}
	s.start()
	if !s.offer(queued("a"), false) || !s.offer(queued("b"), false) {
		t.Fatal("rejected a message while open")
	}
	select {
	case <-s.ready:
	default:
		t.Fatal("offer did not signal ready")
	}
	if got, force := s.take(); len(got) != 2 || force {
		t.Fatalf("take = %d messages (force %v), want 2 unforced", len(got), force)
	}
	s.offer(queued("c"), false)
	if left := s.close(); len(left) != 1 || left[0].Content != "c" {
		t.Fatalf("close returned %v, want the untaken message", left)
	}
	if s.offer(queued("late"), false) {
		t.Fatal("accepted a message after close")
	}
}

// Send now forces a steer on a message the turn holds, and only on those.
func TestSteeringForcesHeldMessages(t *testing.T) {
	s := newSteering()
	s.start()
	s.offer(queued("a"), false)
	if s.forceHeld("other") {
		t.Fatal("forced a message the turn doesn't hold")
	}
	if !s.forceHeld("a") {
		t.Fatal("did not force a held message")
	}
	if _, force := s.take(); !force {
		t.Fatal("take did not report the forced steer")
	}
	if _, force := s.take(); force {
		t.Fatal("force reported twice")
	}
	s.release("a")
	if s.forceHeld("a") {
		t.Fatal("forced a message the CLI already started on")
	}
}

// A message sent while a Claude turn runs goes straight to the turn; once
// the turn can't take it any more it is queued.
func TestEnqueueSteersRunningTurn(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	tr := newTestTurn(m)
	m.turns[testChat] = tr
	tr.steer.start()

	steered, err := m.enqueue(ctx, testChat, testUser, queued("steer me"))
	if err != nil || !steered {
		t.Fatalf("enqueue = %v, %v; want steered", steered, err)
	}
	if got, force := tr.steer.take(); len(got) != 1 || force {
		t.Fatalf("turn got %v (force %v), want the message unforced", got, force)
	}
	if n := queueLen(t, m); n != 0 {
		t.Fatalf("queue has %d messages, want 0", n)
	}

	tr.steer.close()
	if steered, err := m.enqueue(ctx, testChat, testUser, queued("later")); err != nil || steered {
		t.Fatalf("enqueue = %v, %v; want queued", steered, err)
	}
	if n := queueLen(t, m); n != 1 {
		t.Fatalf("queue has %d messages, want 1", n)
	}
}

// Messages queued while the turn was starting go to it once it can take
// them; older ones (left for the user by a stopped turn) stay queued.
func TestSteerQueuedTakesMessagesQueuedSinceTheTurnStarted(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	old := queued("old")
	old.QueuedAt = db.T(time.Now().Add(-time.Minute))
	if err := m.st.enqueue(ctx, testChat, old); err != nil {
		t.Fatal(err)
	}
	tr := newTestTurn(m)
	if err := m.st.enqueue(ctx, testChat, queued("new")); err != nil {
		t.Fatal(err)
	}
	tr.steer.start()
	m.steerQueued(tr)

	if got, _ := tr.steer.take(); len(got) != 1 || got[0].Content != "new" {
		t.Fatalf("turn got %v, want the new message", got)
	}
	if q, _ := m.st.listQueue(ctx, testChat); len(q) != 1 || q[0].Content != "old" {
		t.Fatalf("queue = %v, want the old message", q)
	}
}

// Send now on a running Claude turn forces a steer: the message leaves the
// queue for the turn, and the turn (with its subagents) keeps running.
func TestSendNowSteersRunningTurn(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	if err := m.st.enqueue(ctx, testChat, queued("steer me")); err != nil {
		t.Fatal(err)
	}
	tr := newTestTurn(m)
	tr.steer.start()

	if ok, err := m.sendNowRunning(ctx, tr, testChat, "steer me"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if tr.cancelled.Load() || tr.ctx.Err() != nil {
		t.Fatal("steering interrupted the turn")
	}
	if got, force := tr.steer.take(); len(got) != 1 || got[0].Content != "steer me" || !force {
		t.Fatalf("turn got %v (force %v), want the steered message forced", got, force)
	}
	// Send now again while the turn holds it forces it again; an unknown id is not found.
	if ok, err := m.sendNowRunning(ctx, tr, testChat, "steer me"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if ok, _ := m.sendNowRunning(ctx, tr, testChat, "missing"); ok {
		t.Fatal("found a message nobody holds")
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
	tr := newTestTurn(m)
	tr.steer.close() // past its Claude loop

	if ok, err := m.sendNowRunning(ctx, tr, testChat, "run me"); err != nil || !ok {
		t.Fatal(ok, err)
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
	tr.requeueSteered([]*QueuedMessage{queued("later")}, false)
	if !m.st.hasQueued(ctx, testChat, false) {
		t.Fatal("message not back in the queue")
	}
	if m.st.hasQueued(ctx, testChat, true) {
		t.Fatal("stopped steer would run on its own")
	}
}

// Steers the turn didn't get to go back to the head of the queue in the
// order they were sent.
func TestRequeueSteeredKeepsOrder(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	if err := m.st.enqueue(ctx, testChat, queued("queued")); err != nil {
		t.Fatal(err)
	}
	a, b := queued("a"), queued("b")
	b.QueuedAt = db.T(a.QueuedAt.Add(time.Millisecond))
	newTestTurn(m).requeueSteered([]*QueuedMessage{b, a}, false)

	q, _ := m.st.listQueue(ctx, testChat)
	var got []string
	for _, x := range q {
		got = append(got, x.Content)
	}
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "queued" {
		t.Fatalf("queue = %v, want [a b queued]", got)
	}
}

// A message with other settings than the running turn's is queued to run
// as a turn of its own rather than steered into this one.
func TestEnqueueQueuesMessageWithOtherSettings(t *testing.T) {
	m, _ := newTestModule(t)
	tr := newTestTurn(m)
	m.turns[testChat] = tr
	tr.steer.start()

	q := queued("plan it")
	q.PermissionMode = "plan"
	if steered, err := m.enqueue(context.Background(), testChat, testUser, q); err != nil || steered {
		t.Fatalf("enqueue = %v, %v; want queued", steered, err)
	}
	if steered, _ := m.enqueue(context.Background(), testChat, testUser, queued("same")); !steered {
		t.Fatal("message with the turn's settings not steered")
	}
}

// Send now while a Claude turn is still starting leaves the message queued
// as send-now; the turn forces it in once it opens, instead of being
// interrupted.
func TestSendNowWhileTurnStartsForcesItLater(t *testing.T) {
	m, _ := newTestModule(t)
	ctx := context.Background()
	old := queued("old")
	old.QueuedAt = db.T(time.Now().Add(-time.Minute))
	if err := m.st.enqueue(ctx, testChat, old); err != nil {
		t.Fatal(err)
	}
	tr := newTestTurn(m)

	if ok, err := m.sendNowRunning(ctx, tr, testChat, "old"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if tr.cancelled.Load() {
		t.Fatal("send now cancelled the starting turn")
	}
	tr.steer.start()
	m.steerQueued(tr)
	if got, force := tr.steer.take(); len(got) != 1 || got[0].Content != "old" || !force {
		t.Fatalf("turn got %v (force %v), want the old message forced", got, force)
	}
}
