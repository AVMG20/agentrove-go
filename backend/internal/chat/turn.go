package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

// Event kinds folded into the message's render snapshot (and batched).
var snapshotKinds = map[string]bool{
	"assistant_text": true, "user_steering": true, "assistant_thinking": true, "tool_started": true,
	"tool_completed": true, "tool_failed": true, "prompt_suggestions": true, "system": true, "plan": true,
}

const (
	flushInterval = 200 * time.Millisecond
	flushBatch    = 24
	// After a failed event write, wait this long before trying again.
	flushRetryInterval = time.Second
	// Events kept for retry while the DB keeps failing; older ones are dropped
	// (they stay in the render snapshot, only the live stream misses them).
	maxBufferedEvents = 5000
	// A checkpoint rewrites the whole render, so its interval grows with the
	// render's size: checkpointInterval plus a second per checkpointBytesPerSec,
	// at most checkpointMaxInterval. Clients and crash recovery replay the event
	// log after the snapshot's last_seq, so checkpointMaxLag bounds that replay.
	checkpointInterval    = 2 * time.Second
	checkpointBytesPerSec = 64 << 10
	checkpointMaxInterval = time.Minute
	checkpointMaxLag      = 2000
	interruptGrace        = 20 * time.Second
	memberPermTimeout     = 5 * time.Minute
)

var promptSuggestionsRE = regexp.MustCompile(`(?s)<prompt_suggestions>\s*(.*?)\s*</prompt_suggestions>`)

type turnRequest struct {
	chat           *models.Chat
	ws             *models.Workspace
	userID         string
	prompt         string // the user's text, already wrapped for Claude
	modelID        string
	permissionMode string
	effort         string
	personaName    string
	systemPrompt   string
	fullReplace    bool
	attachments    []models.Attachment
	messageID      string
	isNewChat      bool
	rawPrompt      string // for titling
	followup       bool   // continue a cycle Claude started on its own
	member         func(kind string, payload map[string]any)
	onDone         func(status, content string)
	unattended     bool
}

type pendingPerm struct {
	controlID string
	req       claude.CanUseTool
	timer     *time.Timer
}

type pendingElicit struct {
	controlID string
	req       claude.CanUseTool
	payload   map[string]any
}

type bufferedEvent struct {
	kind    string
	payload map[string]any
}

type turn struct {
	m        *Module
	req      *turnRequest
	chatID   string
	streamID string
	publish  string // user id to publish envelopes to; "" for channel members

	ctx       context.Context
	cancel    context.CancelFunc
	cancelled atomic.Bool
	done      chan struct{}

	in   *inbox
	tr   *claude.Translator
	sess *claude.Session

	mu             sync.Mutex
	buf            []bufferedEvent
	flushTimer     *time.Timer // pending timed flush of buf
	retryAt        time.Time   // no flush before this, after a failed write
	closed         bool        // finished: no more timed flushes
	render         []map[string]any
	renderVersion  int // bumped by applySnapshot
	textParts      []string
	lastSeq        int64
	lastFlush      time.Time
	lastCheckpoint time.Time
	// State of the last checkpoint, to skip or space out rewrites.
	checkpointVersion int
	checkpointSeq     int64
	checkpointBytes   int
	// queueMode is how much of the queue the chat may run once this turn
	// releases it: "" none, "send_now" or "all". Set by finish.
	queueMode   string
	perms       map[string]*pendingPerm
	elicits     map[string]*pendingElicit
	elicitOrder []string
	lastTokens  int
	lastWindow  int
	costUSD     *float64
	// errText is why the turn failed; kept out of the render (the UI shows it
	// from stream_error) but appended to content_text for text readers.
	errText string
}

func newTurn(m *Module, req *turnRequest) *turn {
	ctx, cancel := context.WithCancel(context.Background())
	t := &turn{
		m: m, req: req, chatID: req.chat.ID, streamID: uuid.NewString(),
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		in: newInbox(), tr: claude.NewTranslator(),
		perms: map[string]*pendingPerm{}, elicits: map[string]*pendingElicit{},
		lastFlush: time.Now(), lastCheckpoint: time.Now(),
	}
	if req.member == nil {
		t.publish = req.userID
	}
	t.tr.SetContextWindow(claude.ContextWindow(req.modelID))
	return t
}

// emit records one stream event. Snapshot kinds are buffered and flushed in
// batches (at the latest flushInterval later); everything else is persisted
// and published immediately.
func (t *turn) emit(kind string, payload map[string]any) int64 {
	if t.req.member != nil {
		t.req.member(kind, payload)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	defer t.scheduleFlushLocked()
	t.buf = append(t.buf, bufferedEvent{kind, payload})
	if snapshotKinds[kind] {
		t.applySnapshot(kind, payload)
		if t.closed || len(t.buf) >= flushBatch || time.Since(t.lastFlush) >= flushInterval {
			t.flushLocked()
		}
		return 0
	}
	return t.flushLocked()
}

// scheduleFlushLocked arms a timed flush for whatever is still buffered, so
// an event emitted just before a long tool run (tool_started) goes out
// without waiting for the next event.
func (t *turn) scheduleFlushLocked() {
	if len(t.buf) == 0 || t.flushTimer != nil || t.closed {
		return
	}
	d := flushInterval
	if wait := time.Until(t.retryAt); wait > d {
		d = wait
	}
	t.flushTimer = time.AfterFunc(d, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.flushTimer = nil
		if t.closed {
			return
		}
		t.flushLocked()
		t.scheduleFlushLocked()
	})
}

// closeLocked stops timed flushes at the end of the turn.
func (t *turn) closeLocked() {
	t.closed = true
	if t.flushTimer != nil {
		t.flushTimer.Stop()
		t.flushTimer = nil
	}
}

func (t *turn) applySnapshot(kind string, payload map[string]any) {
	t.renderVersion++
	key := map[string]string{"assistant_text": "text", "assistant_thinking": "thinking"}[kind]
	if kind == "assistant_text" {
		if s, _ := payload["text"].(string); s != "" {
			t.textParts = append(t.textParts, s)
		}
	}
	if key != "" && len(t.render) > 0 && t.render[len(t.render)-1]["type"] == kind {
		last := t.render[len(t.render)-1]
		prev, _ := last[key].(string)
		add, _ := payload[key].(string)
		t.render[len(t.render)-1] = map[string]any{"type": kind, key: prev + add}
		return
	}
	ev := map[string]any{"type": kind}
	for k, v := range payload {
		ev[k] = v
	}
	t.render = append(t.render, ev)
}

// flushLocked persists and publishes the buffer. A failed write keeps the
// events buffered, in order, for the next flush after flushRetryInterval
// (or the next emit once the turn is closed and no timer will retry).
func (t *turn) flushLocked() int64 {
	if len(t.buf) == 0 || (!t.closed && time.Now().Before(t.retryAt)) {
		return t.lastSeq
	}
	batch := t.buf
	t.buf = nil
	kinds := make([]string, len(batch))
	payloads := make([]map[string]any, len(batch))
	for i, e := range batch {
		kinds[i], payloads[i] = e.kind, e.payload
	}
	end, err := t.m.st.appendEvents(context.Background(), t.chatID, t.req.messageID, t.streamID, kinds, payloads, false)
	if err != nil {
		slog.Error("persist stream events; will retry", "chat", t.chatID, "events", len(batch), "err", err)
		if n := len(batch) - maxBufferedEvents; n > 0 {
			slog.Error("dropping unpersisted stream events", "chat", t.chatID, "dropped", n)
			batch = batch[n:]
		}
		t.buf = batch
		t.retryAt = time.Now().Add(flushRetryInterval)
		return t.lastSeq
	}
	t.retryAt = time.Time{}
	t.lastSeq = end
	t.lastFlush = time.Now()
	start := end - int64(len(batch)) + 1
	if t.publish != "" {
		for i := range batch {
			t.m.publishEnvelope(t.publish, t.chatID, t.req.messageID, t.streamID, start+int64(i), kinds[i], payloads[i])
		}
	}
	if t.checkpointDueLocked() {
		t.checkpointLocked("")
	}
	return end
}

// checkpointDueLocked decides whether to rewrite the mid-turn snapshot. The
// event log keeps everything after the snapshot until the turn ends, so a
// skipped checkpoint only means a longer replay, never lost output.
func (t *turn) checkpointDueLocked() bool {
	if t.lastSeq-t.checkpointSeq >= checkpointMaxLag {
		return true
	}
	if t.renderVersion == t.checkpointVersion {
		return false
	}
	interval := checkpointInterval + time.Duration(t.checkpointBytes/checkpointBytesPerSec)*time.Second
	return time.Since(t.lastCheckpoint) >= min(interval, checkpointMaxInterval)
}

func (t *turn) contentText() string {
	raw := strings.Join(t.textParts, "")
	if !strings.Contains(raw, "<prompt_suggestions>") {
		return raw
	}
	return strings.TrimSpace(promptSuggestionsRE.ReplaceAllString(raw, ""))
}

// withErrorText is the content_text of a failed turn: channels, MCP readers
// and search see why it failed, as they did when the error was in the render.
func withErrorText(content, errText string) string {
	switch {
	case errText == "":
		return content
	case content == "":
		return "Error: " + errText
	default:
		return content + "\n\nError: " + errText
	}
}

func (t *turn) renderCopy() map[string]any {
	events := make([]any, len(t.render))
	for i, e := range t.render {
		events[i] = e
	}
	return map[string]any{"events": events}
}

func (t *turn) checkpointLocked(status string) *int64 {
	var active *string
	if status == "" || status == StatusInProgress {
		active = &t.streamID
	}
	render, _ := json.Marshal(t.renderCopy())
	dur, err := t.m.st.updateSnapshot(context.Background(), t.req.messageID, snapshot{
		ContentText: withErrorText(t.contentText(), t.errText), Render: json.RawMessage(render), LastSeq: t.lastSeq,
		ActiveStreamID: active, Status: status, StreamError: t.errText, TotalCostUSD: t.costUSD, RecordActivity: true,
	})
	t.lastCheckpoint = time.Now()
	if err != nil {
		slog.Error("persist message snapshot", "chat", t.chatID, "err", err)
		return dur
	}
	t.checkpointVersion, t.checkpointSeq, t.checkpointBytes = t.renderVersion, t.lastSeq, len(render)
	return dur
}

// run executes the turn to a terminal state, then hands off to any queued
// follow-up. It always finalizes the assistant message.
func (t *turn) run() {
	defer close(t.done)
	start := t.emit("stream_started", map[string]any{"status": "started", "message_id": t.req.messageID})
	t.mu.Lock()
	t.lastSeq = start
	t.checkpointLocked(StatusInProgress)
	t.mu.Unlock()
	if wt := db.Deref(t.req.chat.WorktreeCwd); wt != "" {
		t.emit("system", map[string]any{"data": map[string]any{"worktree_cwd": wt}})
	}
	if t.req.isNewChat && t.req.member == nil && t.req.rawPrompt != "" {
		go t.m.generateTitle(t.req.userID, t.req.chat, t.req.rawPrompt)
	}

	res, err := t.execute()
	switch {
	case t.cancelled.Load():
		t.finish(StatusInterrupted, "")
	case err != nil:
		t.finish(StatusFailed, err.Error())
	case res != nil && res.IsError && !t.tr.SawOutput():
		t.finish(StatusFailed, res.ErrorText)
	case res != nil && res.IsError:
		// Partial output then an error (e.g. max turns): keep the output, show why.
		t.finish(StatusFailed, res.ErrorText)
	default:
		t.finish(StatusCompleted, "")
	}
}

func (t *turn) execute() (*claude.Result, error) {
	sb, err := t.m.a.Sandboxes.For(t.req.ws)
	if err != nil {
		return nil, err
	}
	release := sb.Acquire()
	defer release()

	var sess *claude.Session
	if t.req.followup {
		// Attach to the live process as-is: a config-driven respawn would
		// kill the cycle being streamed.
		if sess = t.m.sessions.get(t.chatID); sess == nil {
			return nil, nil
		}
	} else {
		cfg := t.m.sessionConfig(sb, t.req)
		sess, err = t.m.sessions.acquire(t.ctx, sb, t.chatID, cfg, t.m.wireSession)
		if err != nil && cfg.ResumeID != "" && strings.Contains(err.Error(), "No conversation found") {
			// The transcript is gone (sandbox reset, CLI home wiped): start fresh
			// instead of failing every turn on the stale id.
			slog.Warn("claude session transcript missing; starting a new session", "chat", t.chatID, "session", cfg.ResumeID)
			_, _ = t.m.a.DB.ExecContext(context.Background(), `UPDATE chats SET session_id = NULL WHERE id = ?`, t.chatID)
			cfg.ResumeID = ""
			sess, err = t.m.sessions.acquire(t.ctx, sb, t.chatID, cfg, t.m.wireSession)
		}
		if err != nil {
			if t.cancelled.Load() {
				return nil, nil
			}
			return nil, err
		}
	}
	t.sess = sess
	if !t.req.followup {
		if err := sess.SetModel(t.ctx, t.req.modelID); err != nil && !t.cancelled.Load() {
			return nil, fmt.Errorf("switch model: %w", err)
		}
		if err := sess.SetPermissionMode(t.ctx, t.req.permissionMode); err != nil && !t.cancelled.Load() {
			return nil, fmt.Errorf("switch permission mode: %w", err)
		}
	}
	t.m.attach(t.chatID, t)
	defer t.m.detach(t.chatID, t)

	if sid := sess.SessionID(); sid != "" {
		t.m.persistSessionID(t.chatID, sid)
	}
	// echo is the uuid of our prompt; the CLI replays it when it starts on
	// it. A result before that belongs to a cycle still running ahead of us.
	echo := ""
	if !t.req.followup {
		blocks, err := t.m.promptBlocks(sb, t.req)
		if err != nil {
			return nil, err
		}
		if t.cancelled.Load() {
			return nil, nil
		}
		if echo, err = sess.SendUser(blocks); err != nil {
			return nil, err
		}
	}
	echoSeen := echo == ""
	// handle consumes one message and returns the turn's result, if it ended.
	handle := func(msg claude.Message) *claude.Result {
		if msg.Type == "user" && msg.IsReplay {
			if msg.UUID == echo {
				echoSeen = true
			}
			return nil
		}
		evs, res := t.tr.Handle(msg)
		for _, e := range evs {
			t.process(e)
		}
		if res == nil || (!echoSeen && !t.cancelled.Load()) {
			return nil
		}
		t.mu.Lock()
		cost := sess.CostDelta(res.TotalCostUSD)
		t.costUSD = &cost
		t.mu.Unlock()
		if res.ContextWindow > 0 {
			t.tr.SetContextWindow(res.ContextWindow)
		}
		sess.Touch()
		return res
	}

	var interruptDeadline <-chan time.Time
	ctxDone := t.ctx.Done()
	for {
		select {
		case <-t.in.ready:
			if res := t.drain(handle); res != nil {
				return res, nil
			}
		case <-sess.Done():
			// The reader may have queued the final messages just before exit.
			if res := t.drain(handle); res != nil {
				return res, nil
			}
			if t.cancelled.Load() {
				return nil, nil
			}
			return nil, sess.Err()
		case <-ctxDone:
			ctxDone = nil
			t.denyPending("The user interrupted this turn.")
			go func() {
				ictx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = sess.Interrupt(ictx)
			}()
			interruptDeadline = time.After(interruptGrace)
		case <-interruptDeadline:
			slog.Warn("claude did not stop after interrupt; killing", "chat", t.chatID)
			t.m.sessions.drop(t.chatID, sess)
			return nil, nil
		}
	}
}

// drain handles queued session output until one message ends the turn; the
// rest stays queued for whoever takes the session's output next.
func (t *turn) drain(handle func(claude.Message) *claude.Result) *claude.Result {
	msgs := t.in.take()
	for i, msg := range msgs {
		if res := handle(msg); res != nil {
			t.in.unshift(msgs[i+1:])
			return res
		}
	}
	return nil
}

// inbox queues session output for a turn without ever blocking the
// session's reader.
type inbox struct {
	mu    sync.Mutex
	msgs  []claude.Message
	ready chan struct{}
}

func newInbox() *inbox { return &inbox{ready: make(chan struct{}, 1)} }

func (b *inbox) push(msgs ...claude.Message) {
	b.mu.Lock()
	b.msgs = append(b.msgs, msgs...)
	b.mu.Unlock()
	b.signal()
}

func (b *inbox) unshift(msgs []claude.Message) {
	if len(msgs) == 0 {
		return
	}
	b.mu.Lock()
	b.msgs = append(append([]claude.Message{}, msgs...), b.msgs...)
	b.mu.Unlock()
	b.signal()
}

func (b *inbox) take() []claude.Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.msgs
	b.msgs = nil
	return out
}

func (b *inbox) signal() {
	select {
	case b.ready <- struct{}{}:
	default:
	}
}

// process handles one translated event.
func (t *turn) process(e claude.Event) {
	switch e.Kind {
	case "usage":
		data, _ := e.Payload["data"].(map[string]any)
		tokens, _ := data["input_tokens"].(int)
		window, _ := data["context_window"].(int)
		t.contextUsage(tokens, window)
		return
	case "system":
		if data, ok := e.Payload["data"].(map[string]any); ok {
			if sid, _ := data["session_id"].(string); sid != "" {
				t.m.persistSessionID(t.chatID, sid)
			}
		}
	case "tool_completed":
		if tool, ok := e.Payload["tool"].(map[string]any); ok && t.sess != nil {
			if tool["name"] == "EnterPlanMode" {
				t.sess.NoteMode("plan")
			}
		}
	}
	t.emit(e.Kind, e.Payload)
}

func (t *turn) contextUsage(tokens, window int) {
	if window <= 0 {
		window = claude.ContextWindow(t.req.modelID)
	}
	if tokens <= 0 || (tokens == t.lastTokens && window == t.lastWindow) {
		return
	}
	t.lastTokens, t.lastWindow = tokens, window
	pct := 0.0
	if window > 0 {
		pct = float64(tokens) / float64(window) * 100
		if pct > 100 {
			pct = 100
		}
	}
	_, _ = t.m.a.DB.ExecContext(context.Background(), `UPDATE chats SET context_token_usage = ?, context_window = ? WHERE id = ?`, tokens, window, t.chatID)
	t.emit("system", map[string]any{"context_usage": map[string]any{"tokens_used": tokens, "context_window": window, "percentage": pct}, "chat_id": t.chatID})
}

// handleControl receives the CLI's control requests (permission prompts).
// It must not block: the answer arrives later from an HTTP handler.
func (t *turn) handleControl(controlID string, raw json.RawMessage) {
	subtype, req := claude.ParseControl(raw)
	if subtype == "cancel" {
		t.dismissControl(controlID)
		return
	}
	if subtype != "can_use_tool" {
		t.sess.RespondControl(controlID, map[string]any{})
		return
	}
	if req.ToolName == "AskUserQuestion" {
		if t.req.unattended || t.req.member != nil {
			t.sess.RespondControl(controlID, map[string]any{"behavior": "deny", "message": "No user is available to answer questions. Proceed with your best judgment and state your assumptions.", "interrupt": false})
			return
		}
		t.askQuestion(controlID, req)
		return
	}
	if t.req.unattended {
		t.sess.RespondControl(controlID, map[string]any{"behavior": "allow", "updatedInput": orEmpty(req.Input)})
		return
	}
	requestID := req.ToolUseID
	if requestID == "" {
		requestID = controlID
	}
	p := &pendingPerm{controlID: controlID, req: req}
	t.mu.Lock()
	t.perms[requestID] = p
	t.mu.Unlock()
	if t.req.member != nil {
		p.timer = time.AfterFunc(memberPermTimeout, func() { t.resolvePermission(requestID, "") })
	}
	t.emit("permission_request", map[string]any{
		"request_id": requestID, "tool_name": req.ToolName, "tool_input": orEmpty(req.Input),
		"data": map[string]any{"options": claude.PermissionOptions(req.ToolName)},
	})
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func (t *turn) resolvePermission(requestID, optionID string) bool {
	t.mu.Lock()
	p := t.perms[requestID]
	delete(t.perms, requestID)
	t.mu.Unlock()
	if p == nil {
		return false
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	resp, mode := claude.PermissionDecision(p.req, optionID)
	if mode != "" {
		t.sess.NoteMode(mode)
		if p.req.ToolUseID != "" {
			t.tr.SetResolvedMode(p.req.ToolUseID, mode)
		}
	}
	t.sess.RespondControl(p.controlID, resp)
	if t.req.member != nil {
		t.emit("permission_resolved", map[string]any{"request_id": requestID})
	}
	return true
}

func (t *turn) askQuestion(controlID string, req claude.CanUseTool) {
	msg, schema := claude.QuestionSchema(req.Input)
	t.mu.Lock()
	// Unique across turns: the client ignores a request id it already holds.
	id := "elicit-" + uuid.NewString()
	payload := map[string]any{"message": msg, "tool_call_id": nilIfEmpty(req.ToolUseID), "requested_schema": schema}
	t.elicits[id] = &pendingElicit{controlID: controlID, req: req, payload: payload}
	t.elicitOrder = append(t.elicitOrder, id)
	t.mu.Unlock()
	t.emit("elicitation_request", map[string]any{"request_id": id, "data": payload})
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (t *turn) resolveElicitation(id, action string, content map[string]any) bool {
	t.mu.Lock()
	e := t.elicits[id]
	delete(t.elicits, id)
	t.mu.Unlock()
	if e == nil {
		return false
	}
	if action == "accept" {
		t.sess.RespondControl(e.controlID, map[string]any{"behavior": "allow", "updatedInput": claude.QuestionAnswers(e.req.Input, content)})
	} else {
		msg := "The user declined to answer. Proceed with your best judgment."
		if action == "cancel" {
			msg = "The user dismissed the question."
		}
		t.sess.RespondControl(e.controlID, map[string]any{"behavior": "deny", "message": msg, "interrupt": false})
	}
	return true
}

func (t *turn) pendingElicitations() []map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := []map[string]any{}
	for _, id := range t.elicitOrder {
		e, ok := t.elicits[id]
		if !ok {
			continue
		}
		item := map[string]any{"request_id": id}
		for k, v := range e.payload {
			item[k] = v
		}
		out = append(out, item)
	}
	return out
}

func (t *turn) denyPending(msg string) {
	t.mu.Lock()
	perms := t.perms
	elicits := t.elicits
	t.perms = map[string]*pendingPerm{}
	t.elicits = map[string]*pendingElicit{}
	t.mu.Unlock()
	if t.sess == nil {
		return
	}
	for _, p := range perms {
		if p.timer != nil {
			p.timer.Stop()
		}
		t.sess.RespondControl(p.controlID, map[string]any{"behavior": "deny", "message": msg, "interrupt": true})
	}
	for id, e := range elicits {
		t.sess.RespondControl(e.controlID, map[string]any{"behavior": "deny", "message": msg, "interrupt": true})
		t.emit("elicitation_dismissed", map[string]any{"request_id": id})
	}
}

// dismissControl drops a prompt whose control request the CLI cancelled.
func (t *turn) dismissControl(controlID string) {
	t.mu.Lock()
	var permID, elicitID string
	for id, p := range t.perms {
		if p.controlID == controlID {
			permID = id
			if p.timer != nil {
				p.timer.Stop()
			}
			delete(t.perms, id)
		}
	}
	for id, e := range t.elicits {
		if e.controlID == controlID {
			elicitID = id
			delete(t.elicits, id)
		}
	}
	t.mu.Unlock()
	if permID != "" {
		t.emit("permission_resolved", map[string]any{"request_id": permID})
	}
	if elicitID != "" {
		t.emit("elicitation_dismissed", map[string]any{"request_id": elicitID})
	}
}

// finish persists the terminal snapshot and emits the terminal event, unless
// a queued message takes over the stream.
func (t *turn) finish(status, errText string) {
	if strings.Contains(errText, "Not logged in") {
		errText += "\n\nClaude has no credentials. Add ANTHROPIC_BASE_URL + ANTHROPIC_AUTH_TOKEN (proxy), ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN under Settings → Environment Variables, or run `claude` in a project terminal to log in."
	}
	t.denyPending("The turn ended.")
	t.emitPromptSuggestions()
	t.mu.Lock()
	t.closeLocked() // from here on every emit flushes inline
	t.flushLocked()
	if status == StatusFailed {
		if errText == "" {
			errText = "Stream ended unexpectedly"
		}
		t.errText = errText
	}
	dur := t.checkpointLocked(status)
	content := withErrorText(t.contentText(), t.errText)
	t.mu.Unlock()

	if t.req.onDone != nil {
		defer t.req.onDone(status, content)
	}
	if t.req.member == nil {
		switch status {
		case StatusCompleted:
			t.queueMode = queueAll
		case StatusInterrupted:
			t.queueMode = queueSendNow
		}
	}
	switch status {
	case StatusCompleted:
		if t.req.member == nil && t.m.startQueued(t, false, dur) {
			return
		}
		t.emit("complete", map[string]any{"status": "completed", "duration_ms": dur})
	case StatusInterrupted:
		if t.req.member == nil && t.m.startQueued(t, true, dur) {
			return
		}
		t.emit("cancelled", map[string]any{"status": StatusInterrupted, "duration_ms": dur})
	default:
		t.emit("error", map[string]any{"error": errText})
	}
}

func (t *turn) emitPromptSuggestions() {
	t.mu.Lock()
	raw := strings.Join(t.textParts, "")
	t.mu.Unlock()
	m := promptSuggestionsRE.FindStringSubmatch(raw)
	if m == nil {
		return
	}
	var list []string
	if json.Unmarshal([]byte(m[1]), &list) != nil {
		return
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) > 0 {
		t.emit("prompt_suggestions", map[string]any{"suggestions": out})
	}
}

// envelope is the SSE wire shape (StreamEnvelope in the frontend).
func envelope(chatID, messageID, streamID string, seq int64, kind string, payload map[string]any) []byte {
	if payload == nil {
		payload = map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"chatId": chatID, "messageId": messageID, "streamId": streamID, "seq": seq, "kind": kind, "payload": payload})
	return b
}

func (m *Module) publishEnvelope(userID, chatID, messageID, streamID string, seq int64, kind string, payload map[string]any) {
	m.a.Hub.Publish(events.UserStreams(userID), envelope(chatID, messageID, streamID, seq, kind, payload))
}

func (m *Module) publishChatEvent(userID string, payload map[string]any) {
	b, _ := json.Marshal(payload)
	m.a.Hub.Publish(events.UserChats(userID), b)
}

var errBusy = errors.New("A response is already streaming for this chat. Wait for it to finish or queue the message instead.")
