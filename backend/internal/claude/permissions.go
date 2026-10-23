package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CanUseTool is the CLI's permission request for one tool call.
type CanUseTool struct {
	ToolName    string          `json:"tool_name"`
	Input       map[string]any  `json:"input"`
	Suggestions json.RawMessage `json:"permission_suggestions"`
	ToolUseID   string          `json:"tool_use_id"`
	Description string          `json:"description"`
	BlockedPath string          `json:"blocked_path"`
}

func ParseControl(raw json.RawMessage) (subtype string, req CanUseTool) {
	var head struct {
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(raw, &head)
	if head.Subtype == "can_use_tool" {
		_ = json.Unmarshal(raw, &req)
	}
	return head.Subtype, req
}

// PermissionOption mirrors the option shape the approval UI renders.
type PermissionOption struct {
	Kind           string  `json:"kind"`
	Name           string  `json:"name"`
	OptionID       string  `json:"option_id"`
	PermissionMode *string `json:"permission_mode"`
}

func mode(m string) *string { return &m }

// PermissionOptions returns the choices offered for a tool. ExitPlanMode's
// choices double as the permission mode to continue in.
func PermissionOptions(toolName string) []PermissionOption {
	if toolName == "ExitPlanMode" {
		return []PermissionOption{
			{Kind: "allow_always", Name: "Yes, and bypass permissions", OptionID: "bypassPermissions", PermissionMode: mode("bypassPermissions")},
			{Kind: "allow_always", Name: "Yes, and auto-accept edits", OptionID: "acceptEdits", PermissionMode: mode("acceptEdits")},
			{Kind: "allow_once", Name: "Yes, and manually approve edits", OptionID: "default", PermissionMode: mode("default")},
			{Kind: "reject_once", Name: "No, keep planning", OptionID: "plan", PermissionMode: mode("plan")},
		}
	}
	return []PermissionOption{
		{Kind: "allow_always", Name: "Always Allow", OptionID: "allow_always"},
		{Kind: "allow_once", Name: "Allow", OptionID: "allow"},
		{Kind: "reject_once", Name: "Reject", OptionID: "reject"},
	}
}

// PermissionDecision builds the control response for the chosen option, and
// the permission mode the session continues in (empty when unchanged).
func PermissionDecision(req CanUseTool, optionID string) (resp map[string]any, newMode string) {
	if optionID == "" {
		return map[string]any{"behavior": "deny", "message": "The user dismissed this request.", "interrupt": false}, ""
	}
	allow := func() map[string]any {
		return map[string]any{"behavior": "allow", "updatedInput": orEmpty(req.Input)}
	}
	if req.ToolName == "ExitPlanMode" {
		if optionID == "plan" {
			return map[string]any{"behavior": "deny", "message": "The user wants to keep planning. Refine the plan based on their feedback.", "interrupt": false}, ""
		}
		r := allow()
		r["updatedPermissions"] = []map[string]any{{"type": "setMode", "mode": optionID, "destination": "session"}}
		return r, optionID
	}
	switch optionID {
	case "allow_always":
		r := allow()
		if len(req.Suggestions) > 0 && string(req.Suggestions) != "null" {
			r["updatedPermissions"] = json.RawMessage(req.Suggestions)
		}
		return r, ""
	case "allow":
		return allow(), ""
	default:
		return map[string]any{"behavior": "deny", "message": "The user rejected this tool call.", "interrupt": false}, ""
	}
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// AskUserQuestion is rendered as an elicitation form: one select (or
// multiselect) per question plus an "Other" free-text companion.
type question struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func parseQuestions(input map[string]any) []question {
	b, _ := json.Marshal(input["questions"])
	var qs []question
	_ = json.Unmarshal(b, &qs)
	return qs
}

// QuestionSchema builds the elicitation message and JSON schema.
func QuestionSchema(input map[string]any) (string, map[string]any) {
	qs := parseQuestions(input)
	props := map[string]any{}
	order := []string{}
	for i, q := range qs {
		key := fmt.Sprintf("q%d", i)
		opts := make([]map[string]any, 0, len(q.Options))
		for _, o := range q.Options {
			opt := map[string]any{"const": o.Label, "title": o.Label}
			if o.Description != "" {
				opt["description"] = o.Description
			}
			opts = append(opts, opt)
		}
		title := q.Question
		if q.MultiSelect {
			props[key] = map[string]any{"type": "array", "title": title, "description": q.Header, "items": map[string]any{"anyOf": opts}}
		} else {
			props[key] = map[string]any{"type": "string", "title": title, "description": q.Header, "oneOf": opts}
		}
		props[key+"_other"] = map[string]any{
			"type": "string", "title": "Other",
			"_meta": map[string]any{"_askUserQuestionCustomAnswer": map[string]any{"questionId": key}},
		}
		order = append(order, key, key+"_other")
	}
	msg := "Claude has a question"
	if len(qs) > 1 {
		msg = "Claude has some questions"
	}
	return msg, map[string]any{"type": "object", "properties": props, "x-order": order}
}

// QuestionAnswers converts submitted form content into AskUserQuestion's
// updatedInput (answers keyed by question text).
func QuestionAnswers(input map[string]any, content map[string]any) map[string]any {
	qs := parseQuestions(input)
	answers := map[string]any{}
	for i, q := range qs {
		key := fmt.Sprintf("q%d", i)
		var parts []string
		switch v := content[key].(type) {
		case string:
			if v != "" {
				parts = append(parts, v)
			}
		case []any:
			for _, x := range v {
				if s, ok := x.(string); ok && s != "" {
					parts = append(parts, s)
				}
			}
		}
		if other, ok := content[key+"_other"].(string); ok && strings.TrimSpace(other) != "" {
			parts = append(parts, strings.TrimSpace(other))
		}
		if len(parts) > 0 {
			answers[q.Question] = strings.Join(parts, ", ")
		}
	}
	out := orEmpty(clone(orEmpty(input)))
	out["answers"] = answers
	return out
}
