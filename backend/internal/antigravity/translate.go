package antigravity

import (
	"encoding/json"
	"fmt"
	"strings"

	"agentrove/internal/agentstream"
)

// Translator maps agy's stream-json lines onto stream events:
//
//	{"event":"init","conversation_id":...}
//	{"event":"step_update","step_update":{"step_index":3,"state":"ACTIVE|DONE|ERROR|...",
//	  "step_type":"agent_response|tool|thinking|user_input|...","text_delta":...,
//	  "tool_name":...,"tool_info":{"name","parameters","output"},"usage":{...}}}
//	{"event":"result","result":{"status":"SUCCESS|...","response":...,"error_message":...}}
//
// Tools whose parameters match a Claude card (run_command → Bash, view_file
// → Read, ...) are renamed so that card renders; the rest use the generic card.
type Translator struct {
	sessionID string
	tools     map[int]agentstream.Tool // started tool steps
	order     []int
	textStep  int // step index of the last text delta, -1 if the last event wasn't text
	sawOutput bool
	lastError string
}

func NewTranslator() *Translator {
	return &Translator{tools: map[int]agentstream.Tool{}, textStep: -1}
}

func (t *Translator) SessionID() string { return t.sessionID }
func (t *Translator) SawOutput() bool   { return t.sawOutput }

type line struct {
	Event          string `json:"event"`
	ConversationID string `json:"conversation_id"`
	StepUpdate     *step  `json:"step_update"`
	Result         *struct {
		ConversationID string `json:"conversation_id"`
		Status         string `json:"status"`
		Response       string `json:"response"`
		// agy reports a failed turn in "error" (a string, e.g. "authentication
		// failed or timed out"); error_message is kept for older builds.
		Error        any    `json:"error"`
		ErrorMessage string `json:"error_message"`
		IsError      bool   `json:"is_error"`
	} `json:"result"`
	Error        any    `json:"error"`
	ErrorMessage string `json:"error_message"`
}

type step struct {
	ConversationID string `json:"conversation_id"`
	StepIndex      int    `json:"step_index"`
	State          string `json:"state"`
	StepType       string `json:"step_type"`
	TextDelta      string `json:"text_delta"`
	ThinkingDelta  string `json:"thinking_delta"`
	Thought        string `json:"thought"`
	ToolName       string `json:"tool_name"`
	ErrorMessage   string `json:"error_message"`
	ToolInfo       *struct {
		Name       string         `json:"name"`
		Parameters map[string]any `json:"parameters"`
		Output     any            `json:"output"`
	} `json:"tool_info"`
	Usage *struct {
		InputTokens     int `json:"input_tokens"`
		CacheReadTokens int `json:"cache_read_tokens"`
	} `json:"usage"`
}

func (t *Translator) HandleLine(raw []byte) ([]agentstream.Event, *agentstream.Result) {
	b, ok := agentstream.JSONLine(raw)
	if !ok {
		return nil, nil
	}
	var l line
	if json.Unmarshal(b, &l) != nil {
		return nil, nil
	}
	switch l.Event {
	case "init":
		return t.session(l.ConversationID), nil
	case "step_update":
		if l.StepUpdate == nil {
			return nil, nil
		}
		out := t.session(l.StepUpdate.ConversationID)
		return append(out, t.step(*l.StepUpdate)...), nil
	case "result":
		if l.Result == nil {
			return t.Finish(), &agentstream.Result{IsError: true, ErrorText: firstNonEmpty(t.lastError, "Antigravity ended the turn without a result")}
		}
		out := append(t.session(l.Result.ConversationID), t.Finish()...)
		if strings.EqualFold(l.Result.Status, "SUCCESS") && !l.Result.IsError {
			return out, &agentstream.Result{}
		}
		msg := firstNonEmpty(errorString(l.Result.Error), l.Result.ErrorMessage, t.lastError)
		if msg == "" {
			msg = fmt.Sprintf("Antigravity ended the turn (%s)", strings.ToLower(firstNonEmpty(l.Result.Status, "error")))
		}
		return out, &agentstream.Result{IsError: true, Subtype: strings.ToLower(l.Result.Status), ErrorText: msg}
	case "error":
		t.lastError = firstNonEmpty(l.ErrorMessage, errorString(l.Error))
	}
	return nil, nil
}

func (t *Translator) session(id string) []agentstream.Event {
	if id == "" || id == t.sessionID {
		return nil
	}
	t.sessionID = id
	return []agentstream.Event{agentstream.SessionEvent(id)}
}

func (t *Translator) step(s step) []agentstream.Event {
	failed := s.State == "ERROR" || s.State == "FAILED"
	if failed && s.ErrorMessage != "" {
		t.lastError = s.ErrorMessage
	}
	kind := strings.ToLower(s.StepType)
	switch {
	case kind == "tool" || s.ToolInfo != nil || s.ToolName != "":
		return t.tool(s, failed)
	case strings.Contains(kind, "think") || strings.Contains(kind, "thought"):
		if d := firstNonEmpty(s.ThinkingDelta, s.TextDelta, s.Thought); d != "" {
			t.sawOutput, t.textStep = true, -1
			return []agentstream.Event{agentstream.Thinking(d)}
		}
	case kind == "agent_response" || kind == "planner_response":
		var out []agentstream.Event
		if d := firstNonEmpty(s.ThinkingDelta, s.Thought); d != "" {
			out = append(out, agentstream.Thinking(d))
			t.textStep = -1
		}
		if s.TextDelta != "" {
			text := s.TextDelta
			if t.textStep >= 0 && t.textStep != s.StepIndex {
				text = "\n\n" + text
			}
			t.textStep = s.StepIndex
			out = append(out, agentstream.Text(text))
		}
		if len(out) > 0 {
			t.sawOutput = true
		}
		if s.Usage != nil && s.State == "DONE" {
			if c := s.Usage.InputTokens + s.Usage.CacheReadTokens; c > 0 {
				out = append(out, agentstream.Usage(c, 0))
			}
		}
		return out
	}
	return nil
}

func (t *Translator) tool(s step, failed bool) []agentstream.Event {
	name := s.ToolName
	var params map[string]any
	var output any
	if s.ToolInfo != nil {
		name = firstNonEmpty(s.ToolInfo.Name, name)
		params, output = s.ToolInfo.Parameters, s.ToolInfo.Output
	}
	t.sawOutput, t.textStep = true, -1
	var out []agentstream.Event
	tool, seen := t.tools[s.StepIndex]
	if !seen || (len(tool.Input) == 0 && len(params) > 0) {
		tool = mapTool(fmt.Sprintf("agy-step-%d", s.StepIndex), name, params)
		if !seen {
			t.order = append(t.order, s.StepIndex)
		}
		t.tools[s.StepIndex] = tool
		out = append(out, tool.Started())
	}
	switch s.State {
	case "DONE":
		delete(t.tools, s.StepIndex)
		tool.Result = output
		out = append(out, tool.Completed())
	case "ERROR", "FAILED", "CANCELED", "CANCELLED", "INTERRUPTED":
		delete(t.tools, s.StepIndex)
		tool.Result = output
		tool.Error = firstNonEmpty(s.ErrorMessage, outputText(output))
		if !failed && tool.Error == "" {
			tool.Error = "Cancelled"
		}
		out = append(out, tool.Failed())
	}
	return out
}

// mapTool renames agy tools onto the Claude cards whose input they can fill.
func mapTool(id, name string, params map[string]any) agentstream.Tool {
	get := func(keys ...string) string { return param(params, keys...) }
	tool := agentstream.Tool{ID: id, Name: name, Input: params}
	switch name {
	case "run_command":
		if cmd := get("CommandLine", "command_line", "Command", "command"); cmd != "" {
			tool.Name, tool.Title, tool.Input = "Bash", "`"+cmd+"`", map[string]any{"command": cmd}
		}
	case "view_file", "read_file":
		if p := get("AbsolutePath", "absolute_path", "Path", "path", "file_path"); p != "" {
			tool.Name, tool.Title, tool.Input = "Read", "Read "+p, map[string]any{"file_path": p}
		}
	case "write_to_file":
		if p := get("TargetFile", "target_file", "Path", "file_path"); p != "" {
			tool.Name, tool.Title = "Write", "Write "+p
			tool.Input = map[string]any{"file_path": p, "content": get("CodeContent", "code_content", "Content", "content")}
		}
	case "replace_file_content", "multi_replace_file_content", "sed_file":
		if p := get("TargetFile", "target_file", "Path", "file_path"); p != "" {
			tool.Name, tool.Title, tool.Input = "Edit", "Edit `"+p+"`", map[string]any{"file_path": p}
		}
	case "grep_search":
		if q := get("Query", "query", "Pattern", "pattern"); q != "" {
			tool.Name, tool.Title = "Grep", `grep "`+q+`"`
			tool.Input = map[string]any{"pattern": q, "path": get("SearchPath", "search_path", "DirectoryPath", "path")}
		}
	case "find_by_name":
		if q := get("Pattern", "pattern", "Query", "query"); q != "" {
			tool.Name, tool.Title = "Glob", "Find `"+q+"`"
			tool.Input = map[string]any{"pattern": q, "path": get("SearchDirectory", "search_directory", "DirectoryPath", "path")}
		}
	case "search_web":
		if q := get("query", "Query"); q != "" {
			tool.Name, tool.Title, tool.Input = "WebSearch", q, map[string]any{"query": q}
		}
	case "read_url_content":
		if u := get("Url", "url", "URL"); u != "" {
			tool.Name, tool.Title, tool.Input = "WebFetch", "Fetch "+u, map[string]any{"url": u}
		}
	}
	if tool.Title == "" {
		tool.Title = strings.ReplaceAll(name, "_", " ")
	}
	return tool
}

// param returns the first string parameter among keys; agy's parameter
// casing varies between tools (Path, AbsolutePath, CommandLine).
func param(params map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := params[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func outputText(v any) string {
	switch o := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(o)
	default:
		b, _ := json.Marshal(o)
		return string(b)
	}
}

// Finish closes out tool steps that never finished (interrupt, crash).
func (t *Translator) Finish() []agentstream.Event {
	var out []agentstream.Event
	for _, i := range t.order {
		if tool, ok := t.tools[i]; ok {
			delete(t.tools, i)
			out = append(out, tool.Completed())
		}
	}
	t.order = nil
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" && v != "<nil>" {
			return v
		}
	}
	return ""
}

// errorString reads an "error" field: a message string, or an object with a
// message.
func errorString(v any) string {
	switch e := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(e)
	case map[string]any:
		if m, ok := e["message"].(string); ok {
			return strings.TrimSpace(m)
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}
