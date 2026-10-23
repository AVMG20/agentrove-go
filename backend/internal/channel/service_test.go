package channel

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/auth"
	"agentrove/internal/config"
	"agentrove/internal/db"
	"agentrove/internal/events"
	"agentrove/internal/store"

	"github.com/google/uuid"
)

// fakeRuntime stands in for the chat engineer's turn runtime.
type fakeRuntime struct {
	mu       sync.Mutex
	prompts  map[string][]string // chatID -> prompts
	reply    func(chatID string, n int) string
	block    map[string]chan struct{} // chatID -> released on cancel
	canceled map[string]int
	db       *db.DB
}

func (f *fakeRuntime) start(req app.ChatTurnRequest) (app.ChatTurnResult, error) {
	f.mu.Lock()
	f.prompts[req.ChatID] = append(f.prompts[req.ChatID], req.Prompt)
	n := len(f.prompts[req.ChatID])
	var block chan struct{}
	if f.block != nil {
		block = make(chan struct{})
		f.block[req.ChatID] = block
	}
	f.mu.Unlock()
	msgID := uuid.NewString()
	now := db.Now()
	if _, err := f.db.Exec(`INSERT INTO messages (id, chat_id, role, duration_ms, created_at, updated_at) VALUES (?, ?, 'assistant', 1234, ?, ?)`,
		msgID, req.ChatID, now, now); err != nil {
		return app.ChatTurnResult{}, err
	}
	go func() {
		req.MemberSink("stream_started", map[string]any{"message_id": msgID})
		text := f.reply(req.ChatID, n)
		for _, part := range strings.SplitAfter(text, " ") {
			req.MemberSink("assistant_text", map[string]any{"text": part})
		}
		if block != nil {
			<-block
			req.MemberSink("cancelled", map[string]any{})
			req.OnDone("interrupted", text)
			return
		}
		req.MemberSink("complete", map[string]any{})
		req.OnDone("completed", text)
	}()
	return app.ChatTurnResult{ChatID: req.ChatID, MessageID: "x"}, nil
}

func (f *fakeRuntime) cancel(chatID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canceled[chatID]++
	if b := f.block[chatID]; b != nil {
		close(b)
		delete(f.block, chatID)
	}
	return nil
}

func (f *fakeRuntime) promptsOf(chatID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.prompts[chatID]...)
}

func setup(t *testing.T, rt *fakeRuntime) (*Service, *app.App, string, string) {
	t.Helper()
	debounce = 30 * time.Millisecond
	d, err := db.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	rt.db = d
	keys, _ := auth.NewKeys(strings.Repeat("k", 40))
	a := &app.App{
		Cfg: &config.Config{APIPrefix: "/api/v1", StoragePath: t.TempDir(), MaxUploadSize: 1 << 20},
		DB:  d, Keys: keys, Hub: events.NewHub(), Store: &store.Store{DB: d, Keys: keys},
	}
	a.Hooks.StartChatTurn = rt.start
	a.Hooks.CancelChatTurn = rt.cancel
	ctx := context.Background()
	userID, wsID := "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	now := db.Now()
	if _, err := d.ExecContext(ctx, `INSERT INTO users (id, email, username, hashed_password, created_at, updated_at) VALUES (?, 'a@b.c', 'a', 'x', ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx, `INSERT INTO workspaces (id, name, user_id, sandbox_id, sandbox_provider, workspace_path, created_at, updated_at)
		VALUES (?, 'ws', ?, 'sb', 'host', ?, ?, ?)`, wsID, userID, t.TempDir(), now, now); err != nil {
		t.Fatal(err)
	}
	s := NewService(a)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	})
	return s, a, userID, wsID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func idleNow(s *Service, ch *Channel) bool {
	st := s.stateFor(ch)
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.turns) == 0 && st.timer == nil
}

func TestChannelConversation(t *testing.T) {
	rt := &fakeRuntime{prompts: map[string][]string{}, canceled: map[string]int{}}
	s, a, userID, wsID := setup(t, rt)
	ctx := context.Background()
	ch, err := s.Create(ctx, userID, CreateInput{WorkspaceID: wsID, Name: "debate", Members: []MemberInput{{ModelID: "opus"}, {ModelID: "sonnet"}}})
	if err != nil {
		t.Fatal(err)
	}
	opus, sonnet := ch.Members[0], ch.Members[1]
	if opus.DisplayName != "opus" || sonnet.DisplayName != "sonnet" || opus.PermissionMode != "default" {
		t.Fatalf("members: %+v %+v", opus, sonnet)
	}
	rt.reply = func(chatID string, n int) string {
		if chatID == opus.ChatID {
			return "Hello from opus"
		}
		return "PASS"
	}
	sub := a.Hub.Subscribe(events.UserStreams(userID), 1024)
	defer sub.Close()

	msg, err := s.Post(ctx, ch, "kick off @everyone", nil)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Seq != 1 || msg.Audience != nil || msg.AuthorType != "user" {
		t.Fatalf("user message: %+v", msg)
	}
	// opus replies, which wakes sonnet a second time; sonnet passes twice.
	waitFor(t, "second sonnet turn", func() bool { return len(rt.promptsOf(sonnet.ChatID)) == 2 })
	waitFor(t, "idle", func() bool { return idleNow(s, ch) })

	msgs, err := s.Messages(ctx, ch.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var visible []*Message
	for _, m := range msgs {
		if m.Status != "deleted" {
			visible = append(visible, m)
		}
	}
	if len(visible) != 2 {
		t.Fatalf("want user + opus messages, got %d: %+v", len(visible), visible)
	}
	reply := visible[1]
	if reply.DurationMS == nil || *reply.DurationMS != 1234 {
		t.Fatalf("duration from source message: %v", reply.DurationMS)
	}
	if reply.Content != "Hello from opus" || reply.Status != "completed" || reply.AuthorType != "agent" || *reply.MemberID != opus.ID || reply.Audience != nil {
		t.Fatalf("reply: %+v", reply)
	}

	sp := rt.promptsOf(sonnet.ChatID)
	if !strings.HasPrefix(sp[0], "You are sonnet") || !strings.HasSuffix(sp[0], "[user]: kick off @everyone") {
		t.Fatalf("first sonnet prompt: %q", sp[0])
	}
	if sp[1] != "[opus]: Hello from opus" {
		t.Fatalf("second sonnet prompt should skip the intro: %q", sp[1])
	}
	if len(rt.promptsOf(opus.ChatID)) != 1 {
		t.Fatalf("opus should not see its own message: %v", rt.promptsOf(opus.ChatID))
	}

	// Everything delivered: nothing unseen remains for anyone.
	for _, m := range ch.Members {
		un, err := s.unseen(ctx, m)
		if err != nil || len(un) != 0 {
			t.Fatalf("unseen for %s: %v %v", m.DisplayName, un, err)
		}
	}

	// Live events reached the user's stream topic in the frontend envelope.
	kinds := map[string]int{}
	drain := time.After(200 * time.Millisecond)
loop:
	for {
		select {
		case b := <-sub.C:
			var env struct {
				ChannelID string `json:"channelId"`
				Kind      string `json:"kind"`
			}
			if err := json.Unmarshal(b, &env); err != nil || env.ChannelID != ch.ID {
				t.Fatalf("bad envelope %s", b)
			}
			kinds[env.Kind]++
		case <-drain:
			break loop
		}
	}
	if kinds["channel_message"] < 2 || kinds["channel_member_activity"] < 2 {
		t.Fatalf("events: %v", kinds)
	}
	act := s.MemberActivity(ch)
	if len(act.MemberIDs) != 0 || act.Version < 2 || act.Epoch == "" {
		t.Fatalf("activity: %+v", act)
	}
}

func TestChannelStopCancelsTurns(t *testing.T) {
	rt := &fakeRuntime{prompts: map[string][]string{}, canceled: map[string]int{}, block: map[string]chan struct{}{}}
	rt.reply = func(string, int) string { return "Working on it" }
	s, _, userID, wsID := setup(t, rt)
	ctx := context.Background()
	ch, err := s.Create(ctx, userID, CreateInput{WorkspaceID: wsID, Name: "c", Members: []MemberInput{{ModelID: "haiku", DisplayName: strp("solo")}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post(ctx, ch, "do things", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "streaming message", func() bool {
		msgs, _ := s.Messages(ctx, ch.ID, 0)
		return len(msgs) == 2 && msgs[1].Status == "streaming"
	})
	if act := s.MemberActivity(ch); len(act.MemberIDs) != 1 {
		t.Fatalf("activity while running: %+v", act)
	}
	s.Stop(ctx, ch)
	waitFor(t, "idle after stop", func() bool { return idleNow(s, ch) })
	msgs, _ := s.Messages(ctx, ch.ID, 0)
	if msgs[1].Status != "cancelled" || msgs[1].Content != "Working on it" {
		t.Fatalf("after stop: %+v", msgs[1])
	}
	if rt.canceled[ch.Members[0].ChatID] == 0 {
		t.Fatal("runtime turn was not cancelled")
	}
	// The cancelled turn did not deliver its batch, but stop marked it delivered.
	if un, _ := s.unseen(ctx, ch.Members[0]); len(un) != 0 {
		t.Fatalf("unseen after stop: %v", un)
	}

	// Queue: added while idle it is sent right away.
	rt.mu.Lock()
	rt.block = nil
	rt.mu.Unlock()
	rt.reply = func(string, int) string { return "PASS" }
	q, err := s.queueAdd(ctx, ch.ID, "queued one", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.DispatchQueued(ctx, ch)
	items, _ := s.queueList(ctx, ch.ID)
	if len(items) != 0 {
		t.Fatalf("queue should be drained, has %d (id %s)", len(items), q.ID)
	}
	waitFor(t, "idle after queue", func() bool { return idleNow(s, ch) })

	if err := s.Delete(ctx, ch); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats WHERE channel_id = ?`, ch.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("member chats left: %d", n)
	}
}
