package chat

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"agentrove/internal/app"
	"agentrove/internal/claude"
	"agentrove/internal/events"
)

func bgChanged(taskIDs ...string) claude.Message {
	tasks := make([]map[string]any, 0, len(taskIDs))
	for _, id := range taskIDs {
		tasks = append(tasks, map[string]any{"task_id": id, "description": "npm run dev", "task_type": "local_bash"})
	}
	raw, _ := json.Marshal(map[string]any{"type": "system", "subtype": "background_tasks_changed", "tasks": tasks})
	return claude.Message{Type: "system", Subtype: "background_tasks_changed", Raw: raw}
}

func nextEvent(t *testing.T, sub *events.Subscription) string {
	t.Helper()
	select {
	case b := <-sub.C:
		return string(b)
	case <-time.After(time.Second):
		t.Fatal("no background_tasks event")
		return ""
	}
}

// A respawned CLI (launch config changed between turns) starts with no
// background tasks; the old process's must not keep the chat awake.
func TestRespawnedSessionStartsWithoutStaleBackgroundTasks(t *testing.T) {
	m, _ := newTestModule(t)
	m.a = &app.App{Hub: events.NewHub()}
	m.bgOwner[testChat] = testUser
	sub := m.a.Hub.Subscribe(events.UserChats(testUser), 16)
	defer sub.Close()

	// Never started, so Done never fires: the test drives sessionClosed itself.
	old := &claude.Session{ChatID: testChat}
	m.wireSession(old)
	m.sessionOutput(old, bgChanged("b1"))
	if !m.keepAlive(testChat) {
		t.Fatal("background task should keep the chat alive")
	}
	if ev := nextEvent(t, sub); !strings.Contains(ev, `"b1"`) {
		t.Fatalf("event = %s", ev)
	}

	// Respawn: the new session is wired while the old one is still closing.
	next := &claude.Session{ChatID: testChat}
	m.wireSession(next)
	if m.keepAlive(testChat) {
		t.Fatal("stale background tasks survived the respawn")
	}
	if ev := nextEvent(t, sub); !strings.Contains(ev, `"tasks":[]`) {
		t.Fatalf("tray not cleared: %s", ev)
	}

	// The old process's late output and exit don't touch the new session's state.
	m.sessionOutput(old, bgChanged("b1"))
	m.sessionOutput(next, bgChanged("b2"))
	m.sessionClosed(old)
	m.mu.Lock()
	tasks := m.bgTasks[testChat]
	m.mu.Unlock()
	if len(tasks) != 1 || tasks[0].ID != "b2" {
		t.Fatalf("tasks = %+v, want only b2", tasks)
	}

	// The live session's exit clears them.
	m.sessionClosed(next)
	if m.keepAlive(testChat) {
		t.Fatal("tasks kept after the live session exited")
	}
}
