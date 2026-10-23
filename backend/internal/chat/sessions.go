package chat

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"agentrove/internal/claude"
	"agentrove/internal/sandbox"
)

// sessions keeps one Claude CLI process per chat alive between turns so
// follow-ups skip the spawn + resume cost. Idle processes are closed after
// CHAT_PROCESS_IDLE_TTL; the session id persists, so the next turn resumes.
type sessions struct {
	mu     sync.Mutex
	byChat map[string]*claude.Session
	// Serializes creation per chat.
	creating map[string]*sync.Mutex
}

func newSessions() *sessions {
	return &sessions{byChat: map[string]*claude.Session{}, creating: map[string]*sync.Mutex{}}
}

func (r *sessions) get(chatID string) *claude.Session {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.byChat[chatID]
	if s != nil && s.Closed() {
		delete(r.byChat, chatID)
		return nil
	}
	return s
}

// acquire returns a live session matching cfg, respawning (with --resume)
// when the launch configuration changed or the process died.
func (r *sessions) acquire(ctx context.Context, sb *sandbox.Sandbox, chatID string, cfg claude.Config, wire func(*claude.Session)) (*claude.Session, error) {
	r.mu.Lock()
	lock := r.creating[chatID]
	if lock == nil {
		lock = &sync.Mutex{}
		r.creating[chatID] = lock
	}
	r.mu.Unlock()
	lock.Lock()
	defer lock.Unlock()

	if s := r.get(chatID); s != nil {
		if s.Matches(cfg) {
			return s, nil
		}
		if id := s.SessionID(); id != "" {
			cfg.ResumeID = id
		}
		r.drop(chatID, s)
	}
	s, err := claude.Start(ctx, sb, chatID, cfg)
	if err != nil {
		return nil, err
	}
	wire(s)
	r.mu.Lock()
	r.byChat[chatID] = s
	r.mu.Unlock()
	go func() {
		<-s.Done()
		r.mu.Lock()
		if r.byChat[chatID] == s {
			delete(r.byChat, chatID)
		}
		r.mu.Unlock()
	}()
	return s, nil
}

func (r *sessions) drop(chatID string, s *claude.Session) {
	r.mu.Lock()
	if r.byChat[chatID] == s {
		delete(r.byChat, chatID)
	}
	r.mu.Unlock()
	go s.Close()
}

func (r *sessions) terminate(chatID string) {
	if s := r.get(chatID); s != nil {
		r.drop(chatID, s)
	}
}

func (r *sessions) terminateSandbox(sandboxID string, busy func(chatID string) bool) {
	r.mu.Lock()
	var victims []*claude.Session
	for id, s := range r.byChat {
		if s.SandboxID == sandboxID && !busy(id) {
			victims = append(victims, s)
			delete(r.byChat, id)
		}
	}
	r.mu.Unlock()
	for _, s := range victims {
		go s.Close()
	}
}

func (r *sessions) closeIdle(ttl time.Duration, busy func(chatID string) bool) {
	r.mu.Lock()
	var victims []*claude.Session
	for id, s := range r.byChat {
		if !busy(id) && s.IdleFor() > ttl {
			victims = append(victims, s)
			delete(r.byChat, id)
		}
	}
	r.mu.Unlock()
	for _, s := range victims {
		slog.Debug("closing idle claude session", "chat", s.ChatID)
		go s.Close()
	}
}

func (r *sessions) closeAll() {
	r.mu.Lock()
	all := r.byChat
	r.byChat = map[string]*claude.Session{}
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, s := range all {
		wg.Add(1)
		go func(s *claude.Session) { defer wg.Done(); s.Close() }(s)
	}
	wg.Wait()
}

// backgroundTask is the composer-tray shape of a running background shell/agent.
type backgroundTask struct {
	ID        string  `json:"id"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	ToolID    string  `json:"tool_id"`
	StartedAt float64 `json:"started_at"`
	Stoppable bool    `json:"stoppable"`
}

func parseBackgroundTasks(raw json.RawMessage, prev []backgroundTask) []backgroundTask {
	var msg struct {
		Tasks []struct {
			TaskID      string `json:"task_id"`
			Description string `json:"description"`
			TaskType    string `json:"task_type"`
			ToolUseID   string `json:"tool_use_id"`
		} `json:"tasks"`
	}
	_ = json.Unmarshal(raw, &msg)
	started := map[string]float64{}
	toolIDs := map[string]string{}
	for _, t := range prev {
		started[t.ID] = t.StartedAt
		toolIDs[t.ID] = t.ToolID
	}
	out := make([]backgroundTask, 0, len(msg.Tasks))
	now := float64(time.Now().UnixMilli()) / 1000
	for _, t := range msg.Tasks {
		kind := "agent"
		if t.TaskType == "local_bash" || t.TaskType == "shell" || t.TaskType == "bash" {
			kind = "shell"
		}
		at, ok := started[t.TaskID]
		if !ok {
			at = now
		}
		title := t.Description
		if title == "" {
			title = t.TaskID
		}
		toolID := t.ToolUseID
		if toolID == "" {
			toolID = toolIDs[t.TaskID]
		}
		out = append(out, backgroundTask{ID: t.TaskID, Kind: kind, Title: title, ToolID: toolID, StartedAt: at, Stoppable: true})
	}
	return out
}
