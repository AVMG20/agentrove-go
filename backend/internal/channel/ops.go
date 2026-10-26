package channel

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"agentrove/internal/db"
	"agentrove/internal/httpx"
	"agentrove/internal/models"

	"github.com/google/uuid"
)

type MemberInput struct {
	ModelID        string  `json:"model_id"`
	DisplayName    *string `json:"display_name"`
	PermissionMode *string `json:"permission_mode"`
	Persona        *string `json:"persona"`
	ThinkingMode   *string `json:"thinking_mode"`
}

type CreateInput struct {
	WorkspaceID string        `json:"workspace_id"`
	Name        string        `json:"name"`
	Worktree    bool          `json:"worktree"`
	Branch      *string       `json:"branch"`
	Members     []MemberInput `json:"members"`
}

var baseRefRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)

// normalizeBranch mirrors the BaseBranch validator: blank means none.
func normalizeBranch(b *string) (*string, error) {
	if b == nil {
		return nil, nil
	}
	v := strings.TrimSpace(*b)
	if v == "" {
		return nil, nil
	}
	if !baseRefRE.MatchString(v) || strings.Contains(v, "..") || strings.Contains(v, "//") || strings.Contains(v, "@{") ||
		strings.HasSuffix(v, "/") || strings.HasSuffix(v, ".lock") {
		return nil, httpx.Unprocessable("Invalid base branch name")
	}
	return &v, nil
}

// validateMembers checks members and assigns display names.
func validateMembers(members []MemberInput) ([]string, error) {
	if len(members) == 0 {
		return nil, httpx.Unprocessable("members: List should have at least 1 item")
	}
	used := map[string]bool{}
	for k := range broadcastMentions {
		used[k] = true
	}
	names := make([]string, 0, len(members))
	for i := range members {
		item := &members[i]
		model, ok := models.LookupModel(item.ModelID)
		if !ok {
			return nil, httpx.Unprocessable("Unknown model_id")
		}
		if item.PermissionMode != nil && !models.ValidPermissionMode(*item.PermissionMode) {
			return nil, httpx.BadRequest("Invalid permission mode")
		}
		if item.Persona != nil && utf8.RuneCountInString(*item.Persona) > 100 {
			return nil, httpx.Unprocessable("persona must be at most 100 characters")
		}
		if item.ThinkingMode != nil && utf8.RuneCountInString(*item.ThinkingMode) > 50 {
			return nil, httpx.Unprocessable("thinking_mode must be at most 50 characters")
		}
		var display string
		if item.DisplayName != nil {
			display = strings.TrimSpace(*item.DisplayName)
			if !displayNameRE.MatchString(display) {
				return nil, httpx.Unprocessable("Display name must be 1–32 characters, start with a letter or digit, " +
					"and contain only letters, digits, dots, underscores, or hyphens")
			}
			if broadcastMentions[strings.ToLower(display)] {
				return nil, httpx.Unprocessable(fmt.Sprintf("Display name '%s' is reserved for @%s mentions", display, display))
			}
			if used[strings.ToLower(display)] {
				return nil, httpx.BadRequest("Duplicate display name: " + display)
			}
		} else {
			display = defaultDisplayName(model.Name, used)
		}
		used[strings.ToLower(display)] = true
		names = append(names, display)
	}
	return names, nil
}

func (s *Service) Create(ctx context.Context, userID string, in CreateInput) (*Channel, error) {
	if n := utf8.RuneCountInString(in.Name); n < 1 || n > 255 {
		return nil, httpx.Unprocessable("name must be between 1 and 255 characters")
	}
	if _, err := uuid.Parse(in.WorkspaceID); err != nil {
		return nil, httpx.Unprocessable("workspace_id: Input should be a valid UUID")
	}
	branch, err := normalizeBranch(in.Branch)
	if err != nil {
		return nil, err
	}
	names, err := validateMembers(in.Members)
	if err != nil {
		return nil, err
	}
	ws, err := s.a.Store.GetWorkspace(ctx, userID, in.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if in.Worktree && ws.SandboxID == "" {
		return nil, httpx.BadRequest("Workspace has no sandbox")
	}
	if branch != nil && !in.Worktree {
		sb, err := s.a.Sandboxes.For(ws)
		if err != nil {
			return nil, httpx.BadRequest(err.Error())
		}
		if err := checkout(ctx, sb, *branch); err != nil {
			return nil, err
		}
	}
	now := db.Now()
	// Members run Claude models: on the default Claude profile.
	var profileID, provider any
	if s.a.Profiles != nil {
		p := s.a.Profiles.DefaultFor(ctx, userID, models.ProviderClaude)
		if p == nil {
			return nil, httpx.BadRequest("Channel members run on Claude: add a Claude account under Settings → Providers first.")
		}
		profileID, provider = p.ID, p.Provider
	}
	ch := &Channel{ID: uuid.NewString(), UserID: userID, WorkspaceID: ws.ID, Name: in.Name, Worktree: in.Worktree, Branch: branch, CreatedAt: now, UpdatedAt: now}
	err = s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channels (`+channelCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			ch.ID, ch.UserID, ch.WorkspaceID, ch.Name, ch.Worktree, ch.Branch, now, now); err != nil {
			return err
		}
		for i, item := range in.Members {
			chatID := uuid.NewString()
			if _, err := tx.ExecContext(ctx, `INSERT INTO chats (id, title, user_id, workspace_id, channel_id, profile_id, provider, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				chatID, in.Name, userID, ws.ID, ch.ID, profileID, provider, now, now); err != nil {
				return err
			}
			mode := models.FullAccessPermissionMode
			if item.PermissionMode != nil {
				mode = *item.PermissionMode
			}
			m := &Member{ID: uuid.NewString(), ChannelID: ch.ID, ChatID: chatID, PermissionMode: mode, ModelID: item.ModelID,
				Persona: item.Persona, ThinkingMode: item.ThinkingMode, DisplayName: names[i]}
			if _, err := tx.ExecContext(ctx, `INSERT INTO channel_members (`+memberCols+`, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				m.ID, m.ChannelID, m.ChatID, m.PermissionMode, m.ModelID, m.Persona, m.ThinkingMode, m.DisplayName, false, now, now); err != nil {
				return err
			}
			ch.Members = append(ch.Members, m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.stateFor(ch)
	if err := s.prepareWorktree(ctx, ch); err != nil {
		if derr := s.Delete(context.WithoutCancel(ctx), ch); derr != nil {
			slog.Warn("cleanup failed channel", "channel", ch.ID, "err", derr)
		}
		return nil, err
	}
	return ch, nil
}

// prepareWorktree gives every member chat the anchor chat's worktree.
func (s *Service) prepareWorktree(ctx context.Context, ch *Channel) error {
	if !ch.Worktree {
		return nil
	}
	anchor, err := s.a.Store.GetChatByID(ctx, ch.anchorChatID())
	if err != nil {
		return err
	}
	cwd := db.Deref(anchor.WorktreeCwd)
	if cwd == "" {
		sb, err := s.sandboxFor(ctx, ch)
		if err != nil {
			return err
		}
		if cwd, err = createWorktree(ctx, sb, anchor.ID, ch.Branch); err != nil {
			return err
		}
	}
	_, err = s.a.DB.ExecContext(ctx, `UPDATE chats SET worktree_cwd = ? WHERE channel_id = ?`, cwd, ch.ID)
	return err
}

func (s *Service) Rename(ctx context.Context, ch *Channel, name string) (*Channel, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > 255 {
		return nil, httpx.Unprocessable("name must be between 1 and 255 characters")
	}
	st := s.stateFor(ch)
	st.control.Lock()
	err := s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET name = ?, updated_at = ? WHERE id = ?`, name, db.Now(), ch.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE chats SET title = ? WHERE channel_id = ?`, name, ch.ID)
		return err
	})
	if err == nil {
		st.mu.Lock()
		st.channel.Name = name
		st.mu.Unlock()
	}
	st.control.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, ch.ID, ch.UserID)
}

// cancelActive (mu held) cancels turns and settles them.
func (s *Service) cancelActive(ctx context.Context, st *state, turns []*memberTurn) {
	for _, t := range turns {
		if t.status != turnFinished {
			t.status = turnCancelled
			if err := s.finish(ctx, st, t); err != nil {
				slog.Warn("finish cancelled channel turn", "member", t.member.ID, "err", err)
			}
		}
		if t.isDone() && st.turns[t.member.ID] == t {
			delete(st.turns, t.member.ID)
		}
	}
	s.publishActivity(st)
}

// Post sends a user message: it interrupts the members it addresses (all on
// a broadcast), then fans out after the debounce.
func (s *Service) Post(ctx context.Context, ch *Channel, content string, files []savedFile) (*Message, error) {
	st := s.stateFor(ch)
	st.control.Lock()
	defer st.control.Unlock()
	var turns []*memberTurn
	defer func() {
		s.cancelTurns(turns)
		st.mu.Lock()
		st.paused = false
		s.schedule(st, time.Time{})
		st.mu.Unlock()
	}()
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.deleted {
		return nil, httpx.NotFound("Channel not found")
	}
	st.paused = true
	audience := userAudience(st.channel.Members, content)
	in := map[string]bool{}
	for _, id := range audience {
		in[id] = true
	}
	for _, m := range st.channel.Members {
		if t := st.turns[m.ID]; t != nil && (audience == nil || in[m.ID]) {
			turns = append(turns, t)
		}
	}
	if audience == nil {
		st.retries = map[string]*retry{}
	}
	for _, id := range audience {
		delete(st.retries, id)
	}
	s.cancelActive(ctx, st, turns)
	msg, err := s.insertMessage(ctx, st.channel, content, nil, files, nil, 0)
	if err != nil {
		return nil, err
	}
	s.publishMessage(st.channel, msg)
	s.fanOut(st)
	return msg, nil
}

// stopState pauses the channel, cancels every turn and marks everything
// pending as delivered so members won't answer it.
func (s *Service) stopState(ctx context.Context, st *state) {
	st.mu.Lock()
	st.paused = true
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.timerGen++
	st.retries = map[string]*retry{}
	var turns []*memberTurn
	for _, m := range st.channel.Members {
		if t := st.turns[m.ID]; t != nil {
			turns = append(turns, t)
		}
	}
	s.cancelActive(ctx, st, turns)
	for _, m := range st.channel.Members {
		batch, err := s.unseen(ctx, m)
		if err == nil {
			err = markDelivered(ctx, s.a.DB, m.ID, batch)
		}
		if err != nil {
			slog.Warn("stop channel: mark delivered", "member", m.ID, "err", err)
		}
	}
	st.mu.Unlock()
	s.cancelTurns(turns)
}

func (s *Service) Stop(ctx context.Context, ch *Channel) {
	st := s.stateFor(ch)
	st.control.Lock()
	defer st.control.Unlock()
	s.stopState(ctx, st)
	st.mu.Lock()
	st.paused = false
	st.mu.Unlock()
}

// abortTurns stops waiting on providers and waits for run goroutines to exit.
func abortTurns(ctx context.Context, turns []*memberTurn) {
	for _, t := range turns {
		t.stopWaiting()
	}
	for _, t := range turns {
		select {
		case <-t.done:
		case <-ctx.Done():
			return
		}
	}
}

func (s *Service) Delete(ctx context.Context, ch *Channel) error {
	st := s.stateFor(ch)
	st.control.Lock()
	defer st.control.Unlock()
	st.mu.Lock()
	st.deleted = true
	st.mu.Unlock()
	s.stopState(ctx, st)
	st.mu.Lock()
	var turns []*memberTurn
	for _, t := range st.turns {
		turns = append(turns, t)
	}
	st.mu.Unlock()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	abortTurns(waitCtx, turns)
	cancel()
	for _, m := range ch.Members {
		for _, fn := range s.a.Hooks.OnChatDeleted {
			fn(m.ChatID)
		}
	}
	if ch.Worktree && len(ch.Members) > 0 {
		if anchor, err := s.a.Store.GetChatByID(ctx, ch.anchorChatID()); err == nil && db.Deref(anchor.WorktreeCwd) != "" {
			if sb, err := s.sandboxFor(ctx, ch); err == nil {
				removeWorktree(ctx, sb, *anchor.WorktreeCwd)
			}
		}
	}
	err := s.a.DB.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM chats WHERE channel_id = ?`, ch.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, ch.ID)
		return err
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.states, ch.ID)
	s.mu.Unlock()
	return nil
}

// DispatchQueued sends the next queued message if the channel went idle
// before the message landed in the queue.
func (s *Service) DispatchQueued(ctx context.Context, ch *Channel) {
	st := s.stateFor(ch)
	st.mu.Lock()
	defer st.mu.Unlock()
	s.sendNextQueued(ctx, st)
}

func (s *Service) SendQueuedNow(ctx context.Context, ch *Channel, id string) (*Message, error) {
	item, err := s.queueDelete(ctx, ch.ID, id)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, httpx.NotFound("Queued message not found")
	}
	msg, err := s.Post(ctx, ch, item.Content, item.files())
	if err != nil {
		_ = s.queueRequeue(context.WithoutCancel(ctx), ch.ID, item)
		return nil, err
	}
	s.publish(ch, "channel_queue_sent", map[string]string{"id": item.ID})
	return msg, nil
}

// Activity returns the member chat message behind an agent channel message.
func (s *Service) Activity(ctx context.Context, ch *Channel, messageID string) (map[string]any, error) {
	if _, err := uuid.Parse(messageID); err != nil {
		return nil, httpx.NotFound("Message activity not found")
	}
	var (
		id, chatID, contentText, role, streamStatus string
		contentRender                               string
		lastSeq                                     int64
		activeStreamID, modelID, checkpointID       *string
		durationMS                                  *int64
		createdAt                                   db.Time
	)
	err := s.a.DB.QueryRowContext(ctx, `SELECT m.id, m.chat_id, m.content_text, m.content_render, m.last_seq, m.active_stream_id, m.role,
			m.created_at, m.model_id, m.stream_status, m.duration_ms, cp.id
		FROM channel_messages cm JOIN messages m ON m.id = cm.source_message_id
		LEFT JOIN chat_checkpoints cp ON cp.assistant_message_id = m.id
		WHERE cm.id = ? AND cm.channel_id = ? AND cm.member_id IS NOT NULL AND cm.status != 'deleted' AND m.deleted_at IS NULL`,
		messageID, ch.ID).Scan(&id, &chatID, &contentText, &contentRender, &lastSeq, &activeStreamID, &role, &createdAt, &modelID,
		&streamStatus, &durationMS, &checkpointID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, httpx.NotFound("Message activity not found")
	}
	if err != nil {
		return nil, err
	}
	attachments := []map[string]any{}
	rows, err := s.a.DB.QueryContext(ctx, `SELECT id, file_url, file_type, filename, created_at FROM message_attachments WHERE message_id = ? ORDER BY created_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var aid, url, ft, fn string
		var created db.Time
		if err := rows.Scan(&aid, &url, &ft, &fn, &created); err != nil {
			return nil, err
		}
		attachments = append(attachments, map[string]any{"id": aid, "message_id": id, "file_url": url, "file_type": ft, "filename": fn, "created_at": created})
	}
	return map[string]any{
		"id": id, "chat_id": chatID, "content_text": contentText, "content_render": json.RawMessage(contentRender),
		"last_seq": lastSeq, "active_stream_id": activeStreamID, "role": role, "created_at": createdAt, "model_id": modelID,
		"stream_status": streamStatus, "duration_ms": durationMS, "attachments": attachments, "checkpoint_id": checkpointID,
		"tool_results_omitted": false,
	}, nil
}

// Recover settles messages left streaming by a crash and wakes every
// channel so undelivered messages get answered.
func (s *Service) Recover(ctx context.Context) error {
	msgs, err := s.queryMessages(ctx, `SELECT `+messageCols+` FROM channel_messages WHERE status = 'streaming'`)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		content, status := m.Content, "cancelled"
		if isSilent(m.Content) {
			content, status = "", "deleted"
		}
		if err := writeMessage(ctx, s.a.DB, m, content, status); err != nil {
			return err
		}
	}
	chans, err := s.loadChannels(ctx, ``)
	if err != nil {
		return err
	}
	for _, ch := range chans {
		if len(ch.Members) == 0 {
			continue
		}
		if err := s.prepareWorktree(ctx, ch); err != nil {
			slog.Warn("channel recover: prepare worktree", "channel", ch.ID, "err", err)
		}
		st := s.stateFor(ch)
		st.mu.Lock()
		for _, m := range msgs {
			if m.ChannelID == ch.ID {
				s.publishMessage(ch, m)
			}
		}
		s.fanOut(st)
		st.mu.Unlock()
	}
	return nil
}

// Shutdown cancels every member turn and waits (bounded by ctx) for them.
func (s *Service) Shutdown(ctx context.Context) {
	s.mu.Lock()
	states := make([]*state, 0, len(s.states))
	for _, st := range s.states {
		states = append(states, st)
	}
	s.mu.Unlock()
	var all []*memberTurn
	for _, st := range states {
		st.mu.Lock()
		st.paused = true
		if st.timer != nil {
			st.timer.Stop()
			st.timer = nil
		}
		st.timerGen++
		var turns []*memberTurn
		for _, t := range st.turns {
			turns = append(turns, t)
		}
		s.cancelActive(context.WithoutCancel(ctx), st, turns)
		all = append(all, turns...)
		st.mu.Unlock()
	}
	s.cancelTurns(all)
	abortTurns(ctx, all)
	s.cancel()
}
