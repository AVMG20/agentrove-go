package codex

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentrove/internal/agentstream"
)

// Translator maps `codex exec --json` events onto stream events. Commands
// become Bash cards and web searches WebSearch cards (their inputs match);
// file changes and MCP calls use the generic card; todo lists become plans.
type Translator struct {
	sessionID string
	open      map[string]agentstream.Tool // started, not yet finished items
	order     []string
	sawOutput bool
	lastText  bool // the previous visible event was assistant text
	lastError string
}

func NewTranslator() *Translator {
	return &Translator{open: map[string]agentstream.Tool{}}
}

func (t *Translator) SessionID() string { return t.sessionID }
func (t *Translator) SawOutput() bool   { return t.sawOutput }

type event struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     json.RawMessage `json:"item"`
	Message  string          `json:"message"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type item struct {
	ID               string          `json:"id"`
	Type             string          `json:"type"`
	Text             string          `json:"text"`
	Message          string          `json:"message"`
	Command          string          `json:"command"`
	AggregatedOutput string          `json:"aggregated_output"`
	ExitCode         *int            `json:"exit_code"`
	Status           string          `json:"status"`
	Changes          []fileChange    `json:"changes"`
	Server           string          `json:"server"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Result           *struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent any `json:"structured_content"`
	} `json:"result"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Query string `json:"query"`
	Items []struct {
		Text      string `json:"text"`
		Completed bool   `json:"completed"`
	} `json:"items"`
}

type fileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

func (t *Translator) HandleLine(line []byte) ([]agentstream.Event, *agentstream.Result) {
	raw, ok := agentstream.JSONLine(line)
	if !ok {
		return nil, nil
	}
	var ev event
	if json.Unmarshal(raw, &ev) != nil {
		return nil, nil
	}
	switch ev.Type {
	case "thread.started":
		if ev.ThreadID != "" {
			t.sessionID = ev.ThreadID
			return []agentstream.Event{agentstream.SessionEvent(ev.ThreadID)}, nil
		}
	case "item.started", "item.updated", "item.completed":
		var it item
		if json.Unmarshal(ev.Item, &it) != nil {
			return nil, nil
		}
		return t.item(ev.Type == "item.completed", it), nil
	case "error":
		// Also sent for retries the CLI recovers from; it only counts if the
		// turn then fails without saying why.
		t.lastError = errorText(ev.Message)
	case "turn.completed":
		return t.Finish(), &agentstream.Result{}
	case "turn.failed":
		msg := t.lastError
		if ev.Error != nil && ev.Error.Message != "" {
			msg = errorText(ev.Error.Message)
		}
		if msg == "" {
			msg = "Codex ended the turn with an error"
		}
		return t.Finish(), &agentstream.Result{IsError: true, Subtype: "turn_failed", ErrorText: msg}
	}
	return nil, nil
}

func (t *Translator) item(done bool, it item) []agentstream.Event {
	switch it.Type {
	case "agent_message":
		if !done || it.Text == "" {
			return nil
		}
		text := it.Text
		if t.lastText {
			text = "\n\n" + text
		}
		t.sawOutput, t.lastText = true, true
		return []agentstream.Event{agentstream.Text(text)}
	case "reasoning":
		if !done || strings.TrimSpace(it.Text) == "" {
			return nil
		}
		t.sawOutput, t.lastText = true, false
		return []agentstream.Event{agentstream.Thinking(it.Text)}
	case "todo_list":
		entries := make([]map[string]any, 0, len(it.Items))
		current := false
		for _, todo := range it.Items {
			status := "pending"
			switch {
			case todo.Completed:
				status = "completed"
			case !current:
				status, current = "in_progress", true
			}
			entries = append(entries, map[string]any{"content": todo.Text, "status": status, "priority": "medium"})
		}
		t.sawOutput = true
		return []agentstream.Event{agentstream.Plan(entries)}
	case "error":
		// Non-fatal notices (e.g. missing model metadata); fatal ones end the
		// turn with turn.failed.
		return nil
	}
	tool, ok := toolFor(it)
	if !ok {
		return nil
	}
	t.sawOutput, t.lastText = true, false
	var out []agentstream.Event
	if _, seen := t.open[it.ID]; !seen {
		t.open[it.ID] = tool
		t.order = append(t.order, it.ID)
		out = append(out, tool.Started())
	}
	if !done {
		return out
	}
	delete(t.open, it.ID)
	if tool.Error != "" {
		return append(out, tool.Failed())
	}
	return append(out, tool.Completed())
}

// toolFor maps an item onto a tool card; finished items carry their result.
func toolFor(it item) (agentstream.Tool, bool) {
	failed := it.Status == "failed" || it.Status == "declined"
	switch it.Type {
	case "command_execution":
		cmd := unwrapShell(it.Command)
		tool := agentstream.Tool{ID: it.ID, Name: "Bash", Title: "`" + cmd + "`", Input: map[string]any{"command": cmd}}
		if it.Status == "in_progress" {
			return tool, true
		}
		tool.Result = it.AggregatedOutput
		if it.ExitCode != nil && *it.ExitCode != 0 {
			failed = true
		}
		if failed {
			tool.Error = strings.TrimSpace(it.AggregatedOutput)
			if it.ExitCode != nil {
				tool.Error = strings.TrimSpace(fmt.Sprintf("%s\nExit code %d", tool.Error, *it.ExitCode))
			}
			if it.Status == "declined" {
				tool.Error = "Command was declined"
			}
		}
		return tool, true
	case "file_change":
		paths := make([]string, 0, len(it.Changes))
		changes := make([]map[string]any, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Path)
			changes = append(changes, map[string]any{"path": c.Path, "kind": c.Kind})
		}
		tool := agentstream.Tool{ID: it.ID, Name: "apply_patch", Title: "Edit " + strings.Join(paths, ", "),
			Input: map[string]any{"changes": changes}, Result: "Applied"}
		if failed {
			tool.Error = "Patch failed"
		}
		return tool, true
	case "mcp_tool_call":
		var args map[string]any
		_ = json.Unmarshal(it.Arguments, &args)
		tool := agentstream.Tool{ID: it.ID, Name: "mcp__" + it.Server + "__" + it.Tool, Input: args}
		if it.Result != nil {
			var texts []string
			for _, c := range it.Result.Content {
				if c.Type == "text" {
					texts = append(texts, c.Text)
				}
			}
			if len(texts) > 0 {
				tool.Result = strings.Join(texts, "\n")
			} else {
				tool.Result = it.Result.StructuredContent
			}
		}
		if it.Error != nil && it.Error.Message != "" {
			tool.Error = it.Error.Message
		} else if failed {
			tool.Error = "Tool call failed"
		}
		return tool, true
	case "web_search":
		return agentstream.Tool{ID: it.ID, Name: "WebSearch", Title: it.Query, Input: map[string]any{"query": it.Query}}, true
	}
	return agentstream.Tool{}, false
}

// Finish closes out tools that never reported completion (the turn ended or
// was interrupted around them).
func (t *Translator) Finish() []agentstream.Event {
	var out []agentstream.Event
	for _, id := range t.order {
		if tool, ok := t.open[id]; ok {
			delete(t.open, id)
			out = append(out, tool.Completed())
		}
	}
	t.order = nil
	return out
}

// unwrapShell shows `cat x` instead of `/bin/bash -lc 'cat x'`.
func unwrapShell(cmd string) string {
	for _, sh := range []string{"/bin/bash -lc ", "/bin/zsh -lc ", "/bin/sh -c ", "bash -lc ", "sh -c "} {
		if rest, ok := strings.CutPrefix(cmd, sh); ok {
			rest = strings.TrimSpace(rest)
			if len(rest) >= 2 && rest[0] == '\'' && rest[len(rest)-1] == '\'' {
				return strings.ReplaceAll(rest[1:len(rest)-1], `'\''`, `'`)
			}
			if len(rest) >= 2 && rest[0] == '"' && rest[len(rest)-1] == '"' {
				return rest[1 : len(rest)-1]
			}
			return rest
		}
	}
	return cmd
}

// errorText digs the readable message out of the API error JSON Codex
// forwards as its error string.
func errorText(msg string) string {
	msg = strings.TrimSpace(msg)
	var nested struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if strings.HasPrefix(msg, "{") && json.Unmarshal([]byte(msg), &nested) == nil {
		if nested.Error.Message != "" {
			return nested.Error.Message
		}
		if nested.Message != "" {
			return nested.Message
		}
	}
	return msg
}
