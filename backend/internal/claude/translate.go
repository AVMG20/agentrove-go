package claude

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"agentrove/internal/agentstream"
)

// Event is one frontend stream event (shared by every provider adapter).
type Event = agentstream.Event

// Result summarizes the CLI's terminal `result` message of a turn.
type Result = agentstream.Result

// Translator turns CLI messages of one turn into frontend stream events.
// Tool payloads mirror what the Claude tool cards expect: name/input are the
// Claude tool name and input, result is the tool_result content.
type Translator struct {
	tools       map[string]map[string]any // tool_use_id -> payload
	toolOrder   []string
	blocks      map[int]string // stream block index -> block type (top level)
	planTasks   map[string]map[string]any
	planOrder   []string
	pendingTask map[string]map[string]any // TaskCreate tool_use_id -> input
	// ResolvedModes records the permission mode chosen in a permission prompt,
	// stamped onto that tool's completion (ExitPlanMode → frontend mode switch).
	// Written from HTTP handlers (SetResolvedMode), read on the turn goroutine.
	modesMu       sync.Mutex
	ResolvedModes map[string]string
	contextWindow int
	lastUsage     int
	sawOutput     bool
}

// SetResolvedMode records the permission mode picked for a tool's prompt.
func (t *Translator) SetResolvedMode(toolUseID, mode string) {
	t.modesMu.Lock()
	t.ResolvedModes[toolUseID] = mode
	t.modesMu.Unlock()
}

func NewTranslator() *Translator {
	return &Translator{
		tools:         map[string]map[string]any{},
		blocks:        map[int]string{},
		planTasks:     map[string]map[string]any{},
		pendingTask:   map[string]map[string]any{},
		ResolvedModes: map[string]string{},
	}
}

// SawOutput reports whether the turn produced any visible output.
func (t *Translator) SawOutput() bool { return t.sawOutput }

// hiddenTools render as plan updates instead of tool cards.
var hiddenTools = map[string]bool{"TodoWrite": true, "TaskCreate": true, "TaskUpdate": true, "TaskList": true, "TaskGet": true}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type apiMessage struct {
	Model   string         `json:"model"`
	Content []contentBlock `json:"content"`
	Usage   *usage         `json:"usage"`
}

type usage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	OutputTokens             int `json:"output_tokens"`
}

func (u *usage) context() int {
	if u == nil {
		return 0
	}
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

// Handle translates one message. It returns the events to emit and, for the
// terminal `result` message, a non-nil Result.
func (t *Translator) Handle(m Message) ([]Event, *Result) {
	switch m.Type {
	case "stream_event":
		return t.streamEvent(m), nil
	case "assistant":
		return t.assistant(m), nil
	case "user":
		return t.user(m), nil
	case "system":
		return t.system(m), nil
	case "result":
		return t.result(m)
	}
	return nil, nil
}

func (t *Translator) streamEvent(m Message) []Event {
	if m.ParentToolUseID != nil && *m.ParentToolUseID != "" {
		return nil // subagent internals surface via their tool calls only
	}
	var ev struct {
		Type         string          `json:"type"`
		Index        int             `json:"index"`
		ContentBlock contentBlock    `json:"content_block"`
		Delta        json.RawMessage `json:"delta"`
		Message      apiMessage      `json:"message"`
	}
	if err := json.Unmarshal(m.Event, &ev); err != nil {
		return nil
	}
	switch ev.Type {
	case "message_start":
		t.blocks = map[int]string{}
		if c := ev.Message.Usage.context(); c > 0 {
			return t.usageEvent(c)
		}
	case "content_block_start":
		t.blocks[ev.Index] = ev.ContentBlock.Type
		if ev.ContentBlock.Type == "tool_use" && !hiddenTools[ev.ContentBlock.Name] {
			return []Event{t.toolStarted(ev.ContentBlock.ID, ev.ContentBlock.Name, nil, nil)}
		}
	case "content_block_delta":
		var d struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		}
		_ = json.Unmarshal(ev.Delta, &d)
		switch d.Type {
		case "text_delta":
			if d.Text != "" {
				t.sawOutput = true
				return []Event{{Kind: "assistant_text", Payload: map[string]any{"text": d.Text}}}
			}
		case "thinking_delta":
			if d.Thinking != "" {
				t.sawOutput = true
				return []Event{{Kind: "assistant_thinking", Payload: map[string]any{"thinking": d.Thinking}}}
			}
		}
	}
	return nil
}

// orphan reports a subagent message whose parent tool belongs to an earlier
// turn: background subagents keep running after their turn ended, and their
// internals have no tool card to nest under here.
func (t *Translator) orphan(m Message) bool {
	return m.ParentToolUseID != nil && *m.ParentToolUseID != "" && t.tools[*m.ParentToolUseID] == nil
}

func (t *Translator) assistant(m Message) []Event {
	if t.orphan(m) {
		return nil
	}
	var msg apiMessage
	if err := json.Unmarshal(m.Message, &msg); err != nil {
		return nil
	}
	var parent *string
	if m.ParentToolUseID != nil && *m.ParentToolUseID != "" {
		parent = m.ParentToolUseID
	}
	var out []Event
	for _, b := range msg.Content {
		if b.Type != "tool_use" {
			continue
		}
		input := decodeObject(b.Input)
		switch b.Name {
		case "TodoWrite":
			out = append(out, t.todoPlan(input)...)
			continue
		case "TaskCreate":
			t.pendingTask[b.ID] = input
			continue
		case "TaskUpdate":
			out = append(out, t.taskUpdate(input)...)
			continue
		case "TaskList", "TaskGet":
			continue
		}
		out = append(out, t.toolStarted(b.ID, b.Name, input, parent))
	}
	if parent == nil {
		if c := msg.Usage.context(); c > 0 && c != t.lastUsage {
			out = append(out, t.usageEvent(c)...)
		}
	}
	return out
}

func (t *Translator) toolStarted(id, name string, input map[string]any, parent *string) Event {
	t.sawOutput = true
	p, ok := t.tools[id]
	if !ok {
		p = map[string]any{"id": id, "name": name, "title": ToolTitle(name, input), "status": "started", "parent_id": nil, "input": nil}
		t.tools[id] = p
		t.toolOrder = append(t.toolOrder, id)
	}
	if parent != nil {
		p["parent_id"] = *parent
	}
	if input != nil {
		p["input"] = input
		p["title"] = ToolTitle(name, input)
	}
	return Event{Kind: "tool_started", Payload: map[string]any{"tool": clone(p)}}
}

var taskIDRE = regexp.MustCompile(`#(\d+)`)

func (t *Translator) user(m Message) []Event {
	if t.orphan(m) {
		return nil
	}
	var msg apiMessage
	if err := json.Unmarshal(m.Message, &msg); err != nil {
		return nil
	}
	var out []Event
	for _, b := range msg.Content {
		if b.Type != "tool_result" {
			continue
		}
		if input, ok := t.pendingTask[b.ToolUseID]; ok {
			delete(t.pendingTask, b.ToolUseID)
			if match := taskIDRE.FindStringSubmatch(ResultText(b.Content)); match != nil {
				out = append(out, t.taskCreate(match[1], input)...)
			}
			continue
		}
		p, ok := t.tools[b.ToolUseID]
		if !ok {
			continue
		}
		delete(t.tools, b.ToolUseID)
		t.modesMu.Lock()
		if mode, ok := t.ResolvedModes[b.ToolUseID]; ok {
			p["permission_mode"] = mode
			delete(t.ResolvedModes, b.ToolUseID)
		}
		t.modesMu.Unlock()
		if b.IsError {
			p["status"] = "failed"
			p["error"] = strings.TrimSpace(ResultText(b.Content))
			if p["error"] == "" {
				p["error"] = "Tool execution failed"
			}
			out = append(out, Event{Kind: "tool_failed", Payload: map[string]any{"tool": clone(p)}})
			continue
		}
		p["status"] = "completed"
		p["result"] = resultValue(b.Content)
		out = append(out, Event{Kind: "tool_completed", Payload: map[string]any{"tool": clone(p)}})
	}
	return out
}

func (t *Translator) system(m Message) []Event {
	if m.Subtype == "init" && m.SessionID != "" {
		return []Event{{Kind: "system", Payload: map[string]any{"data": map[string]any{"session_id": m.SessionID}}}}
	}
	return nil
}

func (t *Translator) result(m Message) ([]Event, *Result) {
	var r struct {
		Subtype      string   `json:"subtype"`
		IsError      bool     `json:"is_error"`
		Result       string   `json:"result"`
		Errors       []string `json:"errors"`
		TotalCostUSD float64  `json:"total_cost_usd"`
		ModelUsage   map[string]struct {
			ContextWindow int `json:"contextWindow"`
		} `json:"modelUsage"`
	}
	_ = json.Unmarshal(m.Raw, &r)
	res := &Result{IsError: r.IsError, Subtype: r.Subtype, TotalCostUSD: r.TotalCostUSD}
	for _, mu := range r.ModelUsage {
		if mu.ContextWindow > res.ContextWindow {
			res.ContextWindow = mu.ContextWindow
		}
	}
	if res.ContextWindow > 0 {
		t.contextWindow = res.ContextWindow
	}
	if r.IsError {
		switch {
		case len(r.Errors) > 0:
			res.ErrorText = strings.Join(r.Errors, "\n")
		case r.Result != "":
			res.ErrorText = r.Result
		default:
			res.ErrorText = "Claude ended the turn with an error (" + r.Subtype + ")"
		}
	}
	// Any tool still open never got a result (interrupt) — close it out.
	var out []Event
	for _, id := range t.toolOrder {
		p, ok := t.tools[id]
		if !ok {
			continue
		}
		delete(t.tools, id)
		p["status"] = "completed"
		out = append(out, Event{Kind: "tool_completed", Payload: map[string]any{"tool": clone(p)}})
	}
	return out, res
}

func (t *Translator) usageEvent(contextTokens int) []Event {
	t.lastUsage = contextTokens
	data := map[string]any{"input_tokens": contextTokens}
	if t.contextWindow > 0 {
		data["context_window"] = t.contextWindow
	}
	return []Event{{Kind: "usage", Payload: map[string]any{"data": data}}}
}

// SetContextWindow seeds the window size (from model metadata) for usage events.
func (t *Translator) SetContextWindow(n int) {
	if n > 0 {
		t.contextWindow = n
	}
}

func (t *Translator) todoPlan(input map[string]any) []Event {
	todos, _ := input["todos"].([]any)
	entries := make([]map[string]any, 0, len(todos))
	for _, raw := range todos {
		todo, _ := raw.(map[string]any)
		if todo == nil {
			continue
		}
		entries = append(entries, map[string]any{"content": todo["content"], "status": todo["status"], "priority": "medium"})
	}
	t.sawOutput = true
	return []Event{{Kind: "plan", Payload: map[string]any{"data": map[string]any{"entries": entries}}}}
}

func (t *Translator) taskCreate(id string, input map[string]any) []Event {
	if _, ok := t.planTasks[id]; !ok {
		t.planOrder = append(t.planOrder, id)
	}
	t.planTasks[id] = map[string]any{"content": firstString(input, "subject", "description"), "status": "pending", "priority": "medium"}
	return t.planEvent()
}

func (t *Translator) taskUpdate(input map[string]any) []Event {
	id := fmt.Sprint(input["taskId"])
	entry, ok := t.planTasks[id]
	if !ok {
		return nil
	}
	if status, _ := input["status"].(string); status != "" {
		if status == "deleted" {
			delete(t.planTasks, id)
			return t.planEvent()
		}
		entry["status"] = status
	}
	if subject, _ := input["subject"].(string); subject != "" {
		entry["content"] = subject
	}
	return t.planEvent()
}

func (t *Translator) planEvent() []Event {
	entries := make([]map[string]any, 0, len(t.planTasks))
	ids := append([]string(nil), t.planOrder...)
	sort.SliceStable(ids, func(i, j int) bool {
		a, _ := strconv.Atoi(ids[i])
		b, _ := strconv.Atoi(ids[j])
		return a < b
	})
	for _, id := range ids {
		if e, ok := t.planTasks[id]; ok {
			entries = append(entries, e)
		}
	}
	t.sawOutput = true
	return []Event{{Kind: "plan", Payload: map[string]any{"data": map[string]any{"entries": entries}}}}
}

// resultValue keeps tool_result content the way the tool cards read it: a
// string for text, the raw block list when images are present.
func resultValue(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []map[string]any
	if json.Unmarshal(raw, &blocks) == nil {
		texts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if b["type"] != "text" {
				var v any
				_ = json.Unmarshal(raw, &v)
				return v
			}
			if s, ok := b["text"].(string); ok {
				texts = append(texts, s)
			}
		}
		return strings.Join(texts, "\n")
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// ResultText flattens tool_result content to text.
func ResultText(raw json.RawMessage) string {
	switch v := resultValue(raw).(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func decodeObject(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func clone(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// ToolTitle is the loading label shown before a card renders its own title.
func ToolTitle(name string, input map[string]any) string {
	str := func(k string) string { s, _ := input[k].(string); return s }
	switch name {
	case "Bash":
		if d := str("description"); d != "" {
			return d
		}
		if c := str("command"); c != "" {
			return "`" + c + "`"
		}
	case "Read":
		if p := str("file_path"); p != "" {
			return "Read " + p
		}
	case "Edit", "MultiEdit":
		if p := str("file_path"); p != "" {
			return "Edit `" + p + "`"
		}
	case "Write":
		if p := str("file_path"); p != "" {
			return "Write " + p
		}
	case "Glob":
		if p := str("pattern"); p != "" {
			return "Find `" + p + "`"
		}
	case "Grep":
		if p := str("pattern"); p != "" {
			return "grep \"" + p + "\""
		}
	case "WebFetch":
		if u := str("url"); u != "" {
			return "Fetch " + u
		}
	case "WebSearch":
		if q := str("query"); q != "" {
			return q
		}
	case "Agent", "Task":
		if d := str("description"); d != "" {
			return d
		}
	}
	return name
}
