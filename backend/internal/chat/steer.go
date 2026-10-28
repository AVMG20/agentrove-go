package chat

import (
	"context"
	"log/slog"
	"sync"

	"agentrove/internal/claude"
	"agentrove/internal/sandbox"
)

// Steering: "Send now" on a queued message while a Claude turn runs hands the
// message to that turn instead of interrupting it. The turn writes it to the
// live CLI as a user message; Claude Code takes it at its next step, so
// running tools and foreground subagents carry on (an interrupt would kill
// them). The CLI echoes the message when it starts on it, and that is when it
// shows up in the turn, as a user_steering event.

// steering is a turn's inbox for steered messages. It only accepts them
// while the turn's Claude loop is running and can still send them.
type steering struct {
	mu      sync.Mutex
	open    bool
	waiting []*QueuedMessage
	ready   chan struct{}
}

func newSteering() *steering { return &steering{ready: make(chan struct{}, 1)} }

// offer hands q to the turn; false means the turn can't take it.
func (s *steering) offer(q *QueuedMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return false
	}
	s.waiting = append(s.waiting, q)
	select {
	case s.ready <- struct{}{}:
	default:
	}
	return true
}

func (s *steering) start() {
	s.mu.Lock()
	s.open = true
	s.mu.Unlock()
}

// take returns the messages offered since the last take.
func (s *steering) take() []*QueuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.waiting
	s.waiting = nil
	return out
}

// close stops accepting messages and returns any not yet taken.
func (s *steering) close() []*QueuedMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.open = false
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

// sendSteered writes the messages offered to the turn into the session.
func (t *turn) sendSteered(sb *sandbox.Sandbox, sess *claude.Session, c *cycle) {
	for _, q := range t.steer.take() {
		if t.cancelled.Load() {
			t.requeueSteered(q, false)
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
		t.requeueSteered(q, true)
	}
}

// emitSteered shows a steered message in the turn once Claude starts on it.
func (t *turn) emitSteered(q *QueuedMessage) {
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

// requeueSteered puts a message the turn didn't get to back in the queue.
// sendNow keeps it first in line to run once the turn ends; a stopped turn
// leaves it queued for the user instead.
func (t *turn) requeueSteered(q *QueuedMessage, sendNow bool) {
	q.sendNow = sendNow
	t.m.st.requeue(context.Background(), t.chatID, q)
}

// sendNowRunning handles "Send now" while a turn runs: a Claude turn takes the
// message as a steer; otherwise the turn is interrupted and the message, left
// flagged send-now in the queue, takes over its stream.
func (m *Module) sendNowRunning(ctx context.Context, t *turn, chatID, queuedID string) error {
	q, err := m.st.takeQueued(ctx, chatID, queuedID)
	if err != nil {
		return err
	}
	if q == nil {
		return nil // already taken (the turn ended and started it)
	}
	if t.steer.offer(q) {
		return nil
	}
	m.st.requeue(ctx, chatID, q)
	t.cancelled.Store(true)
	t.cancel()
	return nil
}
