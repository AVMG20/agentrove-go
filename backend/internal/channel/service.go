// Package channel implements multi-agent channels (/api/v1/channels): a group
// chat where each member is a Claude session on a hidden member chat, fed
// batches of new channel messages and replying (or passing) in turn.
package channel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/httpx"
	"agentrove/internal/models"
)

// debounce lets a burst of messages land before members wake (var for tests).
var debounce = 2500 * time.Millisecond

// maxAgentStreak caps consecutive agent messages since the last user message.
// Members that keep @mentioning each other (or answer every broadcast) would
// otherwise ping-pong forever, burning tokens until the user notices. Past the
// cap members only wake for user messages (var for tests).
var maxAgentStreak = 40

const (
	queueRetryDelay = 5 * time.Second
	flushInterval   = 200 * time.Millisecond
	cancelTimeout   = 10 * time.Second
)

// activityEpoch lets clients detect a backend restart (activity versions reset).
var activityEpoch = func() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}()

type turnStatus int

const (
	turnRunning turnStatus = iota
	turnCancelled
	turnFailed
	turnFinished
)

type retry struct {
	delay time.Duration
	until time.Time
}

type memberTurn struct {
	member          *Member
	batch           []*Message
	sourceMessageID *string
	toolCallIDs     map[string]bool
	text            string
	resetSegment    bool
	lastFlush       time.Time
	flushTimer      *time.Timer
	message         *Message
	status          turnStatus

	done         chan struct{} // run goroutine exited
	providerDone chan struct{} // OnDone fired
	providerOnce sync.Once
	abort        chan struct{} // stop waiting for the provider (delete/shutdown)
	abortOnce    sync.Once
}

func (t *memberTurn) isDone() bool {
	select {
	case <-t.done:
		return true
	default:
		return false
	}
}

func (t *memberTurn) stopWaiting() { t.abortOnce.Do(func() { close(t.abort) }) }

type permissionKey struct{ memberID, requestID string }

type state struct {
	channel *Channel
	// mu guards everything below (the Python state.lock); control serializes
	// user actions (post/stop/rename/delete). Never call chat hooks while
	// holding mu: member events arrive on the runtime's goroutine and take mu.
	mu      sync.Mutex
	control sync.Mutex

	turns       map[string]*memberTurn
	timer       *time.Timer
	timerGen    int
	retries     map[string]*retry
	permissions map[permissionKey]map[string]any
	permOrder   []permissionKey
	unavailable map[string]bool

	activeIDs, waitingIDs, retryingIDs []string
	activityVersion                    int
	quietUntil                         time.Time
	paused, deleted                    bool
}

func newState(ch *Channel) *state {
	return &state{
		channel: ch, turns: map[string]*memberTurn{}, retries: map[string]*retry{},
		permissions: map[permissionKey]map[string]any{}, unavailable: map[string]bool{},
		activeIDs: []string{}, waitingIDs: []string{}, retryingIDs: []string{},
	}
}

type Service struct {
	a *app.App

	mu     sync.Mutex
	states map[string]*state
	ctx    context.Context
	cancel context.CancelFunc
}

func NewService(a *app.App) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{a: a, states: map[string]*state{}, ctx: ctx, cancel: cancel}
}

// stateFor returns the live state of ch, creating it on first use.
func (s *Service) stateFor(ch *Channel) *state {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.states[ch.ID]
	if st == nil {
		st = newState(ch)
		s.states[ch.ID] = st
	}
	return st
}

func (s *Service) existingState(id string) *state {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.states[id]
}

// Read renders the ChannelRead shape with live activity.
func (s *Service) Read(ch *Channel) Read {
	st := s.stateFor(ch)
	st.mu.Lock()
	act := memberActivity(st)
	st.mu.Unlock()
	return Read{
		ID: ch.ID, WorkspaceID: ch.WorkspaceID, Name: ch.Name, Worktree: ch.Worktree, Branch: ch.Branch,
		CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt, Members: ch.Members, AnchorChatID: ch.anchorChatID(), Activity: act,
	}
}

func memberActivity(st *state) Activity {
	return Activity{
		Epoch: activityEpoch, Version: st.activityVersion,
		MemberIDs: append([]string{}, st.activeIDs...), WaitingMemberIDs: append([]string{}, st.waitingIDs...),
		RetryingMemberIDs: append([]string{}, st.retryingIDs...),
	}
}

// MemberActivity is the /member-activity payload.
func (s *Service) MemberActivity(ch *Channel) Activity {
	st := s.stateFor(ch)
	st.mu.Lock()
	defer st.mu.Unlock()
	return memberActivity(st)
}

// Permissions lists pending member permission prompts, oldest first.
func (s *Service) Permissions(ch *Channel) []map[string]any {
	st := s.stateFor(ch)
	st.mu.Lock()
	defer st.mu.Unlock()
	out := []map[string]any{}
	for _, k := range st.permOrder {
		out = append(out, st.permissions[k])
	}
	return out
}

// publish sends a channel envelope on the user's multiplexed stream feed.
func (s *Service) publish(ch *Channel, kind string, payload any) {
	b, err := json.Marshal(map[string]any{"channelId": ch.ID, "kind": kind, "payload": payload})
	if err != nil {
		slog.Warn("channel publish encode", "channel", ch.ID, "err", err)
		return
	}
	s.a.Hub.Publish(events.UserStreams(ch.UserID), b)
}

func (s *Service) publishMessage(ch *Channel, m *Message) {
	s.publish(ch, "channel_message", map[string]any{"message": m})
}

func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// publishActivity (mu held) broadcasts member activity when it changed.
func (s *Service) publishActivity(st *state) {
	active := []string{}
	retrying := []string{}
	for _, m := range st.channel.Members {
		if t := st.turns[m.ID]; t != nil && t.status == turnRunning {
			active = append(active, m.ID)
		} else if st.retries[m.ID] != nil {
			retrying = append(retrying, m.ID)
		}
	}
	waiting := []string{}
	seen := map[string]bool{}
	for _, k := range st.permOrder {
		if !seen[k.memberID] {
			seen[k.memberID] = true
			waiting = append(waiting, k.memberID)
		}
	}
	if equalIDs(active, st.activeIDs) && equalIDs(waiting, st.waitingIDs) && equalIDs(retrying, st.retryingIDs) {
		return
	}
	st.activeIDs, st.waitingIDs, st.retryingIDs = active, waiting, retrying
	st.activityVersion++
	s.publish(st.channel, "channel_member_activity", memberActivity(st))
}

// fanOut (mu held) wakes members after the debounce window.
func (s *Service) fanOut(st *state) {
	st.quietUntil = time.Now().Add(debounce)
	s.schedule(st, time.Time{})
}

// schedule (mu held) replaces the pending wake-up.
func (s *Service) schedule(st *state, retryAt time.Time) {
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.timerGen++
	if st.paused || st.deleted {
		return
	}
	at := st.quietUntil
	if retryAt.After(at) {
		at = retryAt
	}
	gen := st.timerGen
	st.timer = time.AfterFunc(max(0, time.Until(at)), func() { s.wake(st, gen) })
}

func (s *Service) wake(st *state, gen int) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if gen != st.timerGen {
		return
	}
	st.timer = nil
	if st.paused || st.deleted {
		return
	}
	ctx := s.ctx
	now := time.Now()
	capped := len(st.channel.Members) > 1 && s.agentStreakExceeded(ctx, st.channel.ID)
	var retryTimes []time.Time
	for _, member := range st.channel.Members {
		if _, ok := models.LookupModel(member.ModelID); !ok {
			if !st.unavailable[member.ID] {
				slog.Warn("skipping channel member: unknown model", "member", member.ID, "model", member.ModelID)
				st.unavailable[member.ID] = true
			}
			continue
		}
		if r := st.retries[member.ID]; r != nil && r.until.After(now) {
			retryTimes = append(retryTimes, r.until)
			continue
		}
		prev := st.turns[member.ID]
		if prev != nil && (prev.status != turnFailed || !prev.isDone()) {
			continue
		}
		var batch []*Message
		err := func() error {
			if prev != nil {
				if err := s.finish(ctx, st, prev); err != nil {
					return err
				}
				delete(st.turns, member.ID)
			}
			var err error
			batch, err = s.unseen(ctx, member)
			return err
		}()
		if err != nil {
			s.backoff(st, member, err)
			retryTimes = append(retryTimes, st.retries[member.ID].until)
			continue
		}
		any := false
		for _, m := range batch {
			// Past the streak cap agent messages stay unanswered context.
			if capped && m.MemberID != nil {
				continue
			}
			if addressed(m, member.ID) {
				any = true
				break
			}
		}
		if !any {
			continue
		}
		t := &memberTurn{
			member: member, batch: batch, toolCallIDs: map[string]bool{}, status: turnRunning,
			done: make(chan struct{}), providerDone: make(chan struct{}), abort: make(chan struct{}),
		}
		st.turns[member.ID] = t
		go s.runTurn(st, t)
	}
	s.publishActivity(st)
	if len(retryTimes) > 0 {
		earliest := retryTimes[0]
		for _, t := range retryTimes[1:] {
			if t.Before(earliest) {
				earliest = t
			}
		}
		s.schedule(st, earliest)
	}
	s.sendNextQueued(ctx, st)
}

// agentStreakExceeded reports whether the agents have talked among themselves
// maxAgentStreak times since the user last spoke.
func (s *Service) agentStreakExceeded(ctx context.Context, channelID string) bool {
	var n int
	err := s.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM channel_messages
		WHERE channel_id = ? AND member_id IS NOT NULL AND status = 'completed'
		AND seq > COALESCE((SELECT MAX(seq) FROM channel_messages WHERE channel_id = ? AND member_id IS NULL), 0)`, channelID, channelID).Scan(&n)
	if err != nil {
		slog.Warn("channel agent streak", "channel", channelID, "err", err)
		return false
	}
	if n >= maxAgentStreak {
		slog.Warn("channel paused: agents replied to each other too many times; waiting for the user", "channel", channelID, "messages", n)
		return true
	}
	return false
}

func idle(st *state) bool {
	return len(st.turns) == 0 && len(st.retries) == 0 && len(st.permissions) == 0 && st.timer == nil && !st.paused && !st.deleted
}

// sendNextQueued (mu held) posts the oldest queued message once idle.
func (s *Service) sendNextQueued(ctx context.Context, st *state) {
	if !idle(st) {
		return
	}
	item, err := s.queuePop(ctx, st.channel.ID)
	if err != nil {
		slog.Warn("read channel queue", "channel", st.channel.ID, "err", err)
		return
	}
	if item == nil {
		return
	}
	msg, err := s.insertMessage(ctx, st.channel, item.Content, nil, item.files(), nil, 0)
	if err != nil {
		_ = s.queueRequeue(ctx, st.channel.ID, item)
		slog.Warn("send queued channel message", "channel", st.channel.ID, "err", err)
		// The channel is idle, so nothing else would wake it to retry.
		s.schedule(st, time.Now().Add(queueRetryDelay))
		return
	}
	s.publishMessage(st.channel, msg)
	s.fanOut(st)
	s.publish(st.channel, "channel_queue_sent", map[string]string{"id": item.ID})
}

func (s *Service) backoff(st *state, member *Member, cause any) {
	delay := 5 * time.Second
	if prev := st.retries[member.ID]; prev != nil {
		delay = min(prev.delay*2, 300*time.Second)
	}
	st.retries[member.ID] = &retry{delay: delay, until: time.Now().Add(delay)}
	slog.Warn("channel member failed; retrying", "member", member.ID, "in", delay, "err", cause)
}

func (s *Service) failTurn(st *state, t *memberTurn, cause any) {
	if t.status != turnFailed && t.status != turnFinished {
		t.status = turnFailed
		s.backoff(st, t.member, cause)
	}
}

func isConflict(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Status == 409
}

func (s *Service) runTurn(st *state, t *memberTurn) {
	ctx := s.ctx
	member := t.member
	st.mu.Lock()
	prompt := buildPrompt(st.channel.Members, member, t.batch)
	st.mu.Unlock()
	var attachments []app.AttachmentInput
	for _, m := range t.batch {
		for _, a := range m.Attachments {
			fp, fn := a.FilePath, a.Filename
			attachments = append(attachments, app.AttachmentInput{FileURL: a.FileURL, FilePath: &fp, FileType: a.FileType, Filename: &fn})
		}
	}
	persona := models.DefaultPersonaName
	if member.Persona != nil && *member.Persona != "" {
		persona = *member.Persona
	}
	req := app.ChatTurnRequest{
		UserID: st.channel.UserID, ChatID: member.ChatID, Prompt: prompt, ModelID: member.ModelID,
		PermissionMode: member.PermissionMode, ThinkingMode: member.ThinkingMode, SelectedPersonaName: persona,
		Attachments: attachments,
		MemberSink:  func(kind string, payload map[string]any) { s.handleEvent(st, t, kind, payload) },
		OnDone: func(status, _ string) {
			st.mu.Lock()
			if t.status == turnRunning {
				switch status {
				case "failed":
					s.failTurn(st, t, "turn failed")
				case "interrupted":
					t.status = turnCancelled
				}
			}
			st.mu.Unlock()
			t.providerOnce.Do(func() { close(t.providerDone) })
		},
	}

	started := false
	var runErr error
	for {
		st.mu.Lock()
		running := t.status == turnRunning
		st.mu.Unlock()
		if !running {
			break
		}
		start := s.a.Hooks.StartChatTurn
		if start == nil {
			runErr = errors.New("chat runtime unavailable")
			break
		}
		_, err := start(req)
		if err == nil {
			started = true
			break
		}
		if !isConflict(err) {
			runErr = err
			break
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-t.abort:
		}
	}
	if started {
		st.mu.Lock()
		running := t.status == turnRunning
		st.mu.Unlock()
		if !running {
			s.cancelChat(member.ChatID)
		}
		select {
		case <-t.providerDone:
		case <-t.abort:
		}
	}

	st.mu.Lock()
	defer st.mu.Unlock()
	if runErr != nil && t.status == turnRunning {
		s.failTurn(st, t, runErr)
	}
	select {
	case <-t.abort:
		if t.status == turnRunning {
			t.status = turnCancelled
		}
	default:
	}
	if err := s.finish(ctx, st, t); err != nil {
		s.failTurn(st, t, err)
	} else if st.turns[member.ID] == t {
		delete(st.turns, member.ID)
	}
	s.publishActivity(st)
	s.schedule(st, time.Time{})
	// Closed under mu so wake never sees a settled turn as still running.
	close(t.done)
}

// cancelChat interrupts a member turn; never call with st.mu held.
func (s *Service) cancelChat(chatID string) {
	cancel := s.a.Hooks.CancelChatTurn
	if cancel == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := cancel(chatID); err != nil {
			slog.Debug("cancel member turn", "chat", chatID, "err", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(cancelTimeout):
	}
}

func (s *Service) cancelTurns(turns []*memberTurn) {
	var wg sync.WaitGroup
	for _, t := range turns {
		wg.Add(1)
		go func(chatID string) {
			defer wg.Done()
			s.cancelChat(chatID)
		}(t.member.ChatID)
	}
	wg.Wait()
}

// text (mu held by caller) appends streamed reply text, flushing at most
// every 200ms; a reply that is still a PASS candidate stays invisible.
func (s *Service) text(st *state, t *memberTurn, delta string) {
	if t.status != turnRunning {
		return
	}
	if t.resetSegment {
		t.text = ""
		t.resetSegment = false
	}
	t.text += delta
	if t.message == nil && isSilent(t.text) {
		return
	}
	delay := time.Until(t.lastFlush.Add(flushInterval))
	if delay <= 0 {
		s.flush(st, t)
	} else if t.flushTimer == nil {
		t.flushTimer = time.AfterFunc(delay, func() {
			st.mu.Lock()
			defer st.mu.Unlock()
			t.flushTimer = nil
			if t.status == turnRunning {
				s.flush(st, t)
			}
		})
	}
}

func (s *Service) flush(st *state, t *memberTurn) {
	ctx := s.ctx
	if t.message == nil {
		if err := s.beginSpeaking(ctx, st, t); err != nil {
			slog.Warn("channel begin speaking", "err", err)
			return
		}
	} else {
		t.message.ToolCallCount = len(t.toolCallIDs)
		content := t.text
		if isSilent(content) {
			content = ""
		}
		if err := writeMessage(ctx, s.a.DB, t.message, content, "streaming"); err != nil {
			slog.Warn("channel flush", "err", err)
			return
		}
		s.publishMessage(st.channel, t.message)
	}
	t.lastFlush = time.Now()
}

func (s *Service) beginSpeaking(ctx context.Context, st *state, t *memberTurn) error {
	id := t.member.ID
	msg, err := s.insertMessage(ctx, st.channel, t.text, &id, nil, t.sourceMessageID, len(t.toolCallIDs))
	if err != nil {
		return err
	}
	t.message = msg
	s.publishMessage(st.channel, msg)
	return nil
}

// finish (mu held) settles a turn: delivers its batch on success, finalizes
// or deletes its message, and wakes the others when it said something.
func (s *Service) finish(ctx context.Context, st *state, t *memberTurn) error {
	if t.status == turnFinished {
		return nil
	}
	s.resolvePermissions(st, t.member.ID, "")
	successful := t.status == turnRunning
	silent := isSilent(t.text)
	if t.message == nil && successful && !silent {
		if err := s.beginSpeaking(ctx, st, t); err != nil {
			return err
		}
	}
	msg := t.message
	err := s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if successful {
			if err := markDelivered(ctx, tx, t.member.ID, t.batch); err != nil {
				return err
			}
		}
		if msg == nil {
			return nil
		}
		msg.ToolCallCount = len(t.toolCallIDs)
		if silent {
			return writeMessage(ctx, tx, msg, "", "deleted")
		}
		if t.sourceMessageID != nil {
			dur, err := s.sourceDuration(ctx, tx, *t.sourceMessageID)
			if err != nil {
				return err
			}
			if dur != nil {
				msg.DurationMS = dur
			}
		}
		status := "cancelled"
		if successful {
			msg.Audience = replyAudience(st.channel.Members, t.member, t.text, t.batch)
			status = "completed"
		}
		if err := writeMessage(ctx, tx, msg, t.text, status); err != nil {
			return err
		}
		if successful {
			now := db.Now()
			if _, err := tx.ExecContext(ctx, `UPDATE channels SET updated_at = ? WHERE id = ?`, now, st.channel.ID); err != nil {
				return err
			}
			st.channel.UpdatedAt = now
		}
		return nil
	})
	if err != nil {
		return err
	}
	t.status = turnFinished
	if t.flushTimer != nil {
		t.flushTimer.Stop()
		t.flushTimer = nil
	}
	if successful {
		delete(st.retries, t.member.ID)
	}
	if msg != nil {
		s.publishMessage(st.channel, msg)
		if successful && !silent {
			s.fanOut(st)
		}
	}
	return nil
}

// resolvePermissions (mu held) drops pending prompts of a member (all, or one).
func (s *Service) resolvePermissions(st *state, memberID, requestID string) {
	kept := st.permOrder[:0]
	for _, k := range st.permOrder {
		if k.memberID == memberID && (requestID == "" || k.requestID == requestID) {
			delete(st.permissions, k)
			s.publish(st.channel, "channel_permission_resolved", map[string]string{"member_id": memberID, "request_id": k.requestID})
			continue
		}
		kept = append(kept, k)
	}
	st.permOrder = kept
	s.publishActivity(st)
}

var introducingKinds = map[string]bool{
	"assistant_text": true, "assistant_thinking": true, "tool_started": true, "tool_completed": true, "tool_failed": true,
}

// handleEvent receives the member turn's stream events (runtime goroutine).
func (s *Service) handleEvent(st *state, t *memberTurn, kind string, payload map[string]any) {
	st.mu.Lock()
	defer st.mu.Unlock()
	// Provider output confirms delivery; bootstrap failures must not consume
	// the introduction.
	if !t.member.Introduced && introducingKinds[kind] {
		if _, err := s.a.DB.ExecContext(s.ctx, `UPDATE channel_members SET introduced = 1, updated_at = ? WHERE id = ?`, db.Now(), t.member.ID); err == nil {
			t.member.Introduced = true
		}
	}
	switch kind {
	case "stream_started":
		if id, _ := payload["message_id"].(string); id != "" {
			t.sourceMessageID = &id
		}
	case "permission_request":
		requestID, _ := payload["request_id"].(string)
		if t.status != turnRunning {
			// The turn is being torn down; its prompt dies with it.
			go s.cancelChat(t.member.ChatID)
			return
		}
		var options any
		if data, ok := payload["data"].(map[string]any); ok {
			options = data["options"]
		}
		pending := map[string]any{
			"member_id": t.member.ID,
			"chat_id":   t.member.ChatID,
			"request": map[string]any{
				"request_id": requestID, "tool_name": payload["tool_name"], "tool_input": payload["tool_input"], "options": options,
			},
		}
		k := permissionKey{t.member.ID, requestID}
		if _, exists := st.permissions[k]; !exists {
			st.permOrder = append(st.permOrder, k)
		}
		st.permissions[k] = pending
		s.publish(st.channel, "channel_permission_request", pending)
		s.publishActivity(st)
	case "permission_resolved":
		requestID, _ := payload["request_id"].(string)
		s.resolvePermissions(st, t.member.ID, requestID)
	case "assistant_text":
		text, _ := payload["text"].(string)
		s.text(st, t, text)
	case "tool_started", "tool_completed", "tool_failed":
		if t.status == turnRunning {
			if tool, ok := payload["tool"].(map[string]any); ok {
				if id, _ := tool["id"].(string); id != "" {
					t.toolCallIDs[id] = true
				}
			}
			if kind == "tool_started" {
				t.resetSegment = true
			}
		}
	case "error":
		if t.status == turnRunning {
			s.failTurn(st, t, payload["error"])
		}
	case "cancelled":
		if t.status == turnRunning {
			t.status = turnCancelled
		}
	}
}
