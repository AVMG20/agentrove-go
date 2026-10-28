package chat

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/models"
	"agentrove/internal/sandbox"
)

// Steering: a message sent while a Claude turn runs goes to that turn
// instead of waiting for it to end. The turn writes it to the live CLI as a
// user message; Claude Code takes it at its next step, so running tools and
// foreground subagents carry on. The CLI echoes the message when it starts on
// it, and that is when it shows up in the turn, as a user_steering event.
//
// "Send now" forces a steer: the turn moves its foreground Bash commands and
// subagents to the background, so they keep running, and then has the CLI
// stop whatever it is still doing and take the message at once (see makeWay).

// steering is a turn's inbox for steered messages. It only accepts them
// while the turn's Claude loop is running and can still send them.
type steering struct {
	mu      sync.Mutex
	open    bool
	closed  bool
	waiting []*QueuedMessage
	// held are the ids of messages offered and not yet started on or handed
	// back; Send now on one of them forces the steer.
	held  map[string]bool
	force bool
	ready chan struct{}
}

func newSteering() *steering {
	return &steering{held: map[string]bool{}, ready: make(chan struct{}, 1)}
}

// offer hands q to the turn, forcing it with force; false means the turn
// can't take it.
func (s *steering) offer(q *QueuedMessage, force bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false
	}
	s.waiting = append(s.waiting, q)
	s.held[q.ID] = true
	s.force = s.force || force
	s.signal()
	return true
}

// forceHeld forces the steer of a message the turn already holds.
func (s *steering) forceHeld(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open || !s.held[id] {
		return false
	}
	s.force = true
	s.signal()
	return true
}

func (s *steering) signal() {
	select {
	case s.ready <- struct{}{}:
	default:
	}
}

// release forgets a message the CLI started on or that went back to the queue.
func (s *steering) release(id string) {
	s.mu.Lock()
	delete(s.held, id)
	s.mu.Unlock()
}

func (s *steering) start() {
	s.mu.Lock()
	s.open = true
	s.mu.Unlock()
}

// starting reports whether the turn has yet to open its inbox.
func (s *steering) starting() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.open && !s.closed
}

// take returns the messages offered since the last take, and whether a
// steer was forced since.
func (s *steering) take() ([]*QueuedMessage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out, force := s.waiting, s.force
	s.waiting, s.force = nil, false
	return out, force
}

// close stops accepting messages and returns any not yet taken.
func (s *steering) close() []*QueuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open, s.closed = false, true
	out := s.waiting
	s.waiting = nil
	return out
}

// cycle tracks which user messages a turn has sent and the CLI hasn't echoed
// yet. Until all are echoed, a result belongs to a cycle running ahead of
// them and doesn't end the turn.
type cycle struct {
	echo     string // the turn's own prompt; "" when it sent none
	echoSeen bool
	steered  map[string]*QueuedMessage
}

func newCycle(echo string) *cycle {
	return &cycle{echo: echo, echoSeen: echo == "", steered: map[string]*QueuedMessage{}}
}

// replayed records the CLI's echo of uuid; it returns the steered message
// the echo belongs to, if any.
func (c *cycle) replayed(uuid string) *QueuedMessage {
	if uuid == c.echo {
		c.echoSeen = true
		return nil
	}
	q := c.steered[uuid]
	delete(c.steered, uuid)
	return q
}

// settled reports whether a result now ends the turn.
func (c *cycle) settled() bool { return c.echoSeen && len(c.steered) == 0 }

// unechoed returns the steered messages the CLI hasn't started on, by uuid.
func (c *cycle) unechoed() map[string]*QueuedMessage {
	out := make(map[string]*QueuedMessage, len(c.steered))
	for id, q := range c.steered {
		out[id] = q
	}
	return out
}

// sendSteered writes the messages offered to the turn into the session; it
// reports whether a steer was forced.
func (t *turn) sendSteered(sb *sandbox.Sandbox, sess *claude.Session, c *cycle) bool {
	msgs, force := t.steer.take()
	for _, q := range msgs {
		if t.cancelled.Load() {
			t.requeueSteered([]*QueuedMessage{q}, false)
			continue
		}
		req := *t.req
		req.prompt, req.attachments = q.Content, q.Attachments
		if t.m.a != nil {
			if settings, err := t.m.a.Store.GetUserSettings(context.Background(), t.req.userID); err == nil {
				req.prompt = prepareUserPrompt(q.Content, settings.CustomInstructions)
			}
		}
		blocks, err := t.m.promptBlocks(sb, &req)
		if err == nil {
			var id string
			if id, err = sess.SendUser(blocks); err == nil {
				c.steered[id] = q
				continue
			}
		}
		slog.Warn("steer queued message; running it after the turn instead", "chat", t.chatID, "err", err)
		t.requeueSteered([]*QueuedMessage{q}, true)
	}
	return force
}

// forceGrace bounds how long a forced steer waits for the running tools to
// move to the background before it stops them.
const forceGrace = 8 * time.Second

// makeWayEvery is how often a forced steer retries: a tool call is only
// backgroundable once the CLI has started its task, a moment after the call.
const makeWayEvery = 500 * time.Millisecond

// makeWay is one step of a forced steer. While main-thread tools run it moves
// them to the background, so the turn takes the steered messages in at its
// next step without ending them; once none run (Claude is thinking or
// writing, or the tools can't be backgrounded by the deadline) it has the CLI
// stop and take the messages now. It reports whether the steer is done.
func (t *turn) makeWay(sess *claude.Session, c *cycle, deadline time.Time) bool {
	if len(c.steered) == 0 {
		return true // the CLI started on every steered message
	}
	if t.tr.ToolsRunning() && time.Now().Before(deadline) && !t.noBackground.Load() {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := sess.BackgroundTasks(ctx); err != nil {
				slog.Warn("background running tasks for a steer", "chat", t.chatID, "err", err)
				t.noBackground.Store(true) // an older CLI: stop them instead
			}
		}()
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sess.InterruptSendNow(ctx); err != nil {
			slog.Warn("interrupt for a steer", "chat", t.chatID, "err", err)
		}
	}()
	return true
}

// emitSteered shows a steered message in the turn once Claude starts on it.
func (t *turn) emitSteered(q *QueuedMessage) {
	t.steer.release(q.ID)
	atts := []map[string]any{}
	if len(q.Attachments) > 0 {
		stored, err := insertAttachments(context.Background(), t.m.st.db, t.req.messageID, q.Attachments)
		if err != nil {
			slog.Warn("store steered attachments", "chat", t.chatID, "err", err)
		}
		for _, a := range stored {
			atts = append(atts, map[string]any{"id": a.ID, "message_id": a.MessageID, "file_url": a.FileURL, "file_type": a.FileType, "filename": a.Filename, "created_at": a.CreatedAt})
		}
	}
	t.emit("user_steering", map[string]any{"queued_message_id": q.ID, "text": q.Content, "attachments": atts})
}

// requeueSteered puts messages the turn didn't get to back at the head of
// the queue, oldest first. sendNow keeps them first in line to run once the
// turn ends; a stopped turn leaves them queued for the user instead.
func (t *turn) requeueSteered(qs []*QueuedMessage, sendNow bool) {
	qs = slices.Clone(qs)
	slices.SortStableFunc(qs, func(a, b *QueuedMessage) int { return a.QueuedAt.Compare(b.QueuedAt.Time) })
	for i := len(qs) - 1; i >= 0; i-- {
		t.steer.release(qs[i].ID)
		qs[i].sendNow = sendNow
		t.m.st.requeue(context.Background(), t.chatID, qs[i])
	}
}

// steerable reports whether q can join the turn: a message with other
// settings (model, effort, permission mode, persona, a new worktree) runs as
// a turn of its own.
func (t *turn) steerable(q *QueuedMessage) bool {
	effort, _ := claude.Effort(q.ThinkingMode)
	return q.ModelID == t.req.modelID && effort == t.req.effort && q.PermissionMode == t.req.permissionMode &&
		q.SelectedPersonaName == t.req.personaName && (!q.Worktree || t.req.chat.WorktreeCwd != nil)
}

// offerSteer hands q to the turn as a steer if it can join it.
func (t *turn) offerSteer(q *QueuedMessage, force bool) bool {
	return t.steerable(q) && t.steer.offer(q, force)
}

// sendNowRunning handles "Send now" while a turn runs: a Claude turn forces
// the message in as a steer (see makeWay); otherwise the turn is interrupted
// and the message, left flagged send-now in the queue, takes over its stream.
// False means the message is neither queued nor held by the turn.
func (m *Module) sendNowRunning(ctx context.Context, t *turn, chatID, queuedID string) (bool, error) {
	if t.steer.forceHeld(queuedID) {
		return true, nil
	}
	if t.cancelled.Load() || (t.steer.starting() && reqProvider(t.req) == models.ProviderClaude) {
		// A stopping turn starts it when it ends; a starting one forces it in
		// once it can take it (steerQueued).
		res, err := m.st.db.ExecContext(ctx, `UPDATE queued_messages SET send_now = 1 WHERE id = ? AND chat_id = ?`, queuedID, chatID)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return t.steer.forceHeld(queuedID), nil
		}
		if !t.steer.starting() {
			m.steerQueued(t) // it opened meanwhile, maybe after its own look at the queue
		}
		return true, nil
	}
	q, err := m.st.takeQueued(ctx, chatID, queuedID)
	if err != nil {
		return false, err
	}
	if q == nil {
		// Taken meanwhile: started by the turn (forced above if it still can be).
		return t.steer.forceHeld(queuedID), nil
	}
	if t.offerSteer(q, true) {
		return true, nil
	}
	q.sendNow = true
	m.st.requeue(ctx, chatID, q)
	t.cancelled.Store(true)
	t.cancel()
	return true, nil
}

// steerQueued hands the turn the messages that arrived while it was still
// starting and couldn't take them yet: those queued since it was created,
// and those flagged send-now. Older ones were left for the user (a stopped
// turn's steers) and stay queued, as do ones that can't join the turn.
func (m *Module) steerQueued(t *turn) {
	ctx := context.Background()
	queue, err := m.st.listQueue(ctx, t.chatID)
	if err != nil {
		slog.Warn("list queue to steer", "chat", t.chatID, "err", err)
		return
	}
	for _, q := range queue {
		if (!q.sendNow && q.QueuedAt.Before(t.created)) || t.cancelled.Load() || !t.steerable(q) {
			continue
		}
		taken, err := m.st.takeQueued(ctx, t.chatID, q.ID)
		if err != nil || taken == nil {
			continue
		}
		if !t.steer.offer(taken, taken.sendNow) {
			m.st.requeue(ctx, t.chatID, taken)
			return
		}
	}
}
