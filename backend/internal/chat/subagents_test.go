package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentrove/internal/claude"
)

func cliMsg(typ, parent string, blocks ...map[string]any) claude.Message {
	raw, _ := json.Marshal(map[string]any{"role": typ, "content": blocks})
	m := claude.Message{Type: typ, Message: raw}
	if parent != "" {
		m.ParentToolUseID = &parent
	}
	return m
}

func agentCall(id string, background bool) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": "Agent", "input": map[string]any{
		"description": "Build fixes", "prompt": "Fix the editor", "subagent_type": "builder", "run_in_background": background}}
}

func toolResult(id, text string, isError bool) map[string]any {
	return map[string]any{"type": "tool_result", "tool_use_id": id, "content": text, "is_error": isError}
}

func systemMsg(subtype string, fields map[string]any) claude.Message {
	fields["type"], fields["subtype"] = "system", subtype
	raw, _ := json.Marshal(fields)
	return claude.Message{Type: "system", Subtype: subtype, Raw: raw}
}

func TestForegroundSubagentLogsActivityAndResult(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("a1", false)))
	r.handle(testChat, cliMsg("user", "a1", map[string]any{"type": "text", "text": "Fix the editor"}))
	r.handle(testChat, cliMsg("assistant", "a1",
		map[string]any{"type": "text", "text": "Looking at the editor\nmore"},
		map[string]any{"type": "tool_use", "id": "t1", "name": "Read", "input": map[string]any{"file_path": "/a.go"}}))
	r.handle(testChat, cliMsg("user", "a1", toolResult("t1", "ok", false)))
	r.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "tool_use", "id": "t2", "name": "Bash", "input": map[string]any{"command": "go test"}}))
	r.handle(testChat, cliMsg("user", "a1", toolResult("t2", "boom", true)))

	s, ok := r.get(testChat, "a1")
	if !ok || s.Status != subagentRunning || s.Prompt != "Fix the editor" || s.SubagentType != "builder" {
		t.Fatalf("spawned subagent = %+v", s)
	}
	if s.ToolCount != 2 || len(s.Activity) != 3 || s.LastActivity != "`go test`" {
		t.Fatalf("activity = %+v", s)
	}
	if s.Activity[0].Text != "Looking at the editor\nmore" || s.Activity[1].Status != "completed" || s.Activity[2].Status != "failed" {
		t.Fatalf("activity entries = %+v", s.Activity)
	}

	r.handle(testChat, cliMsg("user", "", toolResult("a1", "All fixed", false)))
	s, _ = r.get(testChat, "a1")
	if s.Status != subagentCompleted || s.Result != "All fixed" || s.FinishedAt == nil {
		t.Fatalf("finished subagent = %+v", s)
	}
	// Output after it finished is ignored.
	r.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "text", "text": "late"}))
	if s, _ = r.get(testChat, "a1"); len(s.Activity) != 3 {
		t.Fatalf("late output logged: %+v", s.Activity)
	}
	if sum := s.summary(); sum.Prompt != "" || sum.Activity != nil {
		t.Fatalf("summary carries detail: %+v", sum)
	}
}

func TestBackgroundSubagentFinishesOnNotification(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("a1", true)))
	launched := cliMsg("user", "", toolResult("a1", "Async agent launched", false))
	launched.ToolUseResult = json.RawMessage(`{"status":"async_launched","agentId":"ag1"}`)
	r.handle(testChat, launched)
	// The turn ends; a background subagent keeps going.
	r.handle(testChat, claude.Message{Type: "result"})
	r.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "text", "text": "Done: fixed 4 bugs"}))
	s, _ := r.get(testChat, "a1")
	if s.Status != subagentRunning || !s.Background || s.TaskID != "ag1" {
		t.Fatalf("launched subagent = %+v", s)
	}
	r.handle(testChat, systemMsg("task_notification", map[string]any{"task_id": "ag1", "status": "completed", "summary": "Agent done"}))
	s, _ = r.get(testChat, "a1")
	if s.Status != subagentCompleted || s.Result != "Done: fixed 4 bugs" {
		t.Fatalf("notified subagent = %+v", s)
	}
}

func TestBackgroundSubagentFollowsTaskList(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("a1", false)))
	r.syncTasks(testChat, []backgroundTask{{ID: "task9", Kind: "agent", ToolID: "a1"}})
	r.handle(testChat, claude.Message{Type: "result"})
	s, _ := r.get(testChat, "a1")
	if s.Status != subagentRunning || s.TaskID != "task9" || !s.Background {
		t.Fatalf("listed subagent = %+v", s)
	}
	r.syncTasks(testChat, nil)
	if s, _ = r.get(testChat, "a1"); s.Status != subagentCompleted {
		t.Fatalf("subagent that left the list = %+v", s)
	}
	// A notification still corrects the inferred outcome.
	r.handle(testChat, systemMsg("task_notification", map[string]any{"task_id": "task9", "status": "failed", "summary": "x"}))
	if s, _ = r.get(testChat, "a1"); s.Status != subagentFailed {
		t.Fatalf("corrected subagent = %+v", s)
	}
}

func TestSubagentsStopWithTurnOrProcess(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("fg", false), agentCall("bg", true)))
	r.handle(testChat, claude.Message{Type: "result"})
	if s, _ := r.get(testChat, "fg"); s.Status != subagentStopped {
		t.Fatalf("foreground subagent after its turn = %+v", s)
	}
	if s, _ := r.get(testChat, "bg"); s.Status != subagentRunning {
		t.Fatalf("background subagent after its turn = %+v", s)
	}
	r.stopAll(testChat)
	if s, _ := r.get(testChat, "bg"); s.Status != subagentStopped {
		t.Fatalf("background subagent after process exit = %+v", s)
	}
}

func TestNestedSubagentWorkJoinsItsRoot(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("a1", false)))
	r.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "tool_use", "id": "n1", "name": "Agent", "input": map[string]any{"description": "inner"}}))
	r.handle(testChat, cliMsg("assistant", "n1", map[string]any{"type": "tool_use", "id": "t1", "name": "Grep", "input": map[string]any{"pattern": "x"}}))
	s, _ := r.get(testChat, "a1")
	if s.ToolCount != 2 || s.Activity[1].Name != "Grep" {
		t.Fatalf("nested activity = %+v", s.Activity)
	}
	if _, ok := r.get(testChat, "n1"); ok {
		t.Fatal("nested agent listed as its own subagent")
	}
}

func TestSubagentActivityIsCapped(t *testing.T) {
	r := newSubagentTracker()
	r.handle(testChat, cliMsg("assistant", "", agentCall("a1", false)))
	long := strings.Repeat("x", maxSubagentText*2)
	for i := 0; i < maxSubagentActivity+20; i++ {
		r.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "text", "text": long}))
	}
	s, _ := r.get(testChat, "a1")
	if len(s.Activity) != maxSubagentActivity || len([]rune(s.Activity[0].Text)) != maxSubagentText+1 {
		t.Fatalf("activity len %d, text len %d", len(s.Activity), len([]rune(s.Activity[0].Text)))
	}
}

func TestSubagentsPersistAndReconcile(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()
	m.subs.handle(testChat, cliMsg("assistant", "", agentCall("a1", true)))
	m.subs.handle(testChat, cliMsg("assistant", "a1", map[string]any{"type": "text", "text": "working"}))
	m.flushSubagents(ctx)
	if subs, _ := m.subs.takeDirty(); len(subs) != 0 {
		t.Fatalf("flush left %d dirty", len(subs))
	}
	var status, activity, prompt string
	if err := d.QueryRow(`SELECT status, activity, prompt FROM chat_subagents WHERE chat_id = ? AND id = 'a1'`, testChat).Scan(&status, &activity, &prompt); err != nil {
		t.Fatal(err)
	}
	if status != subagentRunning || !strings.Contains(activity, "working") || prompt != "Fix the editor" {
		t.Fatalf("stored %s %s %s", status, activity, prompt)
	}
	m.reconcileSubagents(ctx)
	if err := d.QueryRow(`SELECT status FROM chat_subagents WHERE id = 'a1'`).Scan(&status); err != nil || status != subagentStopped {
		t.Fatalf("reconciled status %q, %v", status, err)
	}
}
