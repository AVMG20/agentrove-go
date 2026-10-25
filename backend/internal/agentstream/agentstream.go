// Package agentstream holds the provider-neutral shape of a turn's output:
// the frontend stream events every agent adapter (Claude, Codex,
// Antigravity) translates its CLI output into, and the turn's result.
package agentstream

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Event is one frontend stream event: kind plus payload (the envelope body).
type Event struct {
	Kind    string
	Payload map[string]any
}

// Result summarizes how a turn ended.
type Result struct {
	IsError       bool
	Subtype       string
	ErrorText     string
	TotalCostUSD  float64
	ContextWindow int
}

// LineTranslator turns one CLI's stdout lines of a turn into stream events.
// A non-nil Result ends the turn.
type LineTranslator interface {
	HandleLine(line []byte) ([]Event, *Result)
	// SessionID is the provider's thread/conversation id, once known.
	SessionID() string
	// SawOutput reports whether the turn produced any visible output.
	SawOutput() bool
	// Finish closes out tools left open when the process ended.
	Finish() []Event
}

// Text is an assistant text delta.
func Text(s string) Event {
	return Event{Kind: "assistant_text", Payload: map[string]any{"text": s}}
}

// Thinking is an assistant thinking delta.
func Thinking(s string) Event {
	return Event{Kind: "assistant_thinking", Payload: map[string]any{"thinking": s}}
}

// Usage reports the context the last request used.
func Usage(contextTokens, window int) Event {
	data := map[string]any{"input_tokens": contextTokens}
	if window > 0 {
		data["context_window"] = window
	}
	return Event{Kind: "usage", Payload: map[string]any{"data": data}}
}

// SessionEvent announces the provider session id (persisted on the chat).
func SessionEvent(id string) Event {
	return Event{Kind: "system", Payload: map[string]any{"data": map[string]any{"session_id": id}}}
}

// Plan replaces the turn's task list. Status is pending, in_progress or completed.
func Plan(entries []map[string]any) Event {
	return Event{Kind: "plan", Payload: map[string]any{"data": map[string]any{"entries": entries}}}
}

// Tool is the payload the tool cards render: name and input follow the
// Claude tool names where an adapter maps onto one (Bash, Read, ...), so
// those cards render; anything else falls back to the generic card.
type Tool struct {
	ID     string
	Name   string
	Title  string
	Input  map[string]any
	Result any
	Error  string
}

func (t Tool) payload(status string) map[string]any {
	title := t.Title
	if title == "" {
		title = t.Name
	}
	p := map[string]any{"id": t.ID, "name": t.Name, "title": title, "status": status, "parent_id": nil, "input": t.Input}
	switch status {
	case "completed":
		p["result"] = t.Result
	case "failed":
		p["error"] = firstNonEmpty(t.Error, "Tool execution failed")
		if t.Result != nil {
			p["result"] = t.Result
		}
	}
	return p
}

// Started, Completed and Failed are the tool lifecycle events.
func (t Tool) Started() Event {
	return Event{Kind: "tool_started", Payload: map[string]any{"tool": t.payload("started")}}
}

func (t Tool) Completed() Event {
	return Event{Kind: "tool_completed", Payload: map[string]any{"tool": t.payload("completed")}}
}

func (t Tool) Failed() Event {
	return Event{Kind: "tool_failed", Payload: map[string]any{"tool": t.payload("failed")}}
}

// ReadLines calls fn for every newline-terminated line of r (the last one
// may lack the newline). Lines may be long (file contents in tool output).
func ReadLines(r io.Reader, fn func([]byte)) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			fn(line)
		}
		if err != nil {
			return
		}
	}
}

// JSONLine trims a stdout line and reports whether it looks like a JSON object.
func JSONLine(line []byte) ([]byte, bool) {
	s := strings.TrimSpace(string(line))
	if s == "" || s[0] != '{' {
		return nil, false
	}
	return []byte(s), true
}

// Summarize reduces an event to the fields the frontend keys on (adapter
// tests compare these).
func Summarize(e Event) string {
	switch e.Kind {
	case "assistant_text":
		return fmt.Sprintf("text %q", e.Payload["text"])
	case "assistant_thinking":
		return fmt.Sprintf("thinking %q", e.Payload["thinking"])
	case "usage":
		d := e.Payload["data"].(map[string]any)
		return fmt.Sprintf("usage %v/%v", d["input_tokens"], d["context_window"])
	case "system":
		d, _ := e.Payload["data"].(map[string]any)
		return fmt.Sprintf("system session=%v", d["session_id"])
	case "plan":
		s := "plan"
		for _, en := range e.Payload["data"].(map[string]any)["entries"].([]map[string]any) {
			s += fmt.Sprintf(" [%v:%v]", en["content"], en["status"])
		}
		return s
	case "tool_started", "tool_completed", "tool_failed":
		tool := e.Payload["tool"].(map[string]any)
		s := fmt.Sprintf("%s %v %v %q", e.Kind, tool["id"], tool["name"], tool["title"])
		if in, ok := tool["input"].(map[string]any); ok && len(in) > 0 {
			s += fmt.Sprintf(" in=%v", in)
		}
		if r, ok := tool["result"]; ok && r != nil {
			s += fmt.Sprintf(" result=%q", fmt.Sprint(r))
		}
		if e, ok := tool["error"]; ok {
			s += fmt.Sprintf(" error=%q", e)
		}
		return s
	}
	return e.Kind
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
