package mcpserver

import (
	"context"
	"encoding/json"

	"agentrove/internal/automation"
	"agentrove/internal/models"
)

// --- automations ---

// maxAgentAutomations bounds how many schedules agents can leave behind.
const maxAgentAutomations = 20

// agentOwned loads an automation an agent may change: the user's own are
// read-only here (they could otherwise be rewritten into a hidden
// persistent prompt the user still trusts).
func (m *Module) agentOwned(ctx context.Context, c *caller, id string) error {
	x, err := m.automations.Get(ctx, id, c.userID)
	if err != nil {
		return err
	}
	if !x.CreatedByAgent {
		return errorf("Automation %q was created by the user; only the user can change or delete it (in Automations).", x.Name)
	}
	return nil
}

func automationSummary(x *automation.Automation) map[string]any {
	return map[string]any{
		"id": x.ID, "name": x.Name, "prompt": x.Prompt, "cron_expression": x.CronExpression, "timezone": x.Timezone,
		"enabled": x.Enabled, "model_id": x.ModelID, "workspace_id": x.WorkspaceID, "thinking_mode": x.ThinkingMode,
		"worktree": x.Worktree, "persona": x.SelectedPersonaName, "next_run_at": x.NextRunAt, "last_run_at": x.LastRunAt,
		"created_by_agent": x.CreatedByAgent,
	}
}

func (m *Module) listAutomations(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	list, err := m.automations.List(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, x := range list {
		out = append(out, automationSummary(x))
	}
	return map[string]any{"automations": out}, nil
}

func (m *Module) createAutomation(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	a := struct {
		Name           string  `json:"name"`
		Prompt         string  `json:"prompt"`
		CronExpression string  `json:"cron_expression"`
		Timezone       string  `json:"timezone"`
		WorkspaceID    *string `json:"workspace_id"`
		ModelID        *string `json:"model_id"`
		ThinkingMode   *string `json:"thinking_mode"`
		Worktree       bool    `json:"worktree"`
		Persona        *string `json:"persona"`
		Enabled        *bool   `json:"enabled"`
	}{Timezone: "UTC"}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if err := validEffort(a.ThinkingMode); err != nil {
		return nil, err
	}
	model, err := resolveModel(a.ModelID)
	if err != nil {
		return nil, err
	}
	if n, err := m.automations.CountByAgent(ctx, c.userID); err != nil {
		return nil, err
	} else if n >= maxAgentAutomations {
		return nil, errorf("Agents can keep at most %d automations; delete one first.", maxAgentAutomations)
	}
	ws, err := m.resolveWorkspace(ctx, c.userID, a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	mode := models.FullAccessPermissionMode
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	in := &automation.Input{
		WorkspaceID: &ws, Name: &a.Name, Prompt: &a.Prompt, ModelID: &model, CronExpression: &a.CronExpression,
		Timezone: &a.Timezone, PermissionMode: &mode, Worktree: &a.Worktree, Enabled: &enabled, Set: map[string]bool{},
		CreatedByAgent: true,
	}
	if a.ThinkingMode != nil && *a.ThinkingMode != "" {
		in.ThinkingMode = a.ThinkingMode
	}
	if a.Persona != nil && *a.Persona != "" {
		in.SelectedPersonaName = a.Persona
	}
	x, err := m.automations.Create(ctx, c.userID, in, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"automation": automationSummary(x)}, nil
}

func (m *Module) updateAutomation(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		AutomationID   string  `json:"automation_id"`
		Name           *string `json:"name"`
		Prompt         *string `json:"prompt"`
		CronExpression *string `json:"cron_expression"`
		Timezone       *string `json:"timezone"`
		WorkspaceID    *string `json:"workspace_id"`
		ModelID        *string `json:"model_id"`
		ThinkingMode   *string `json:"thinking_mode"`
		Worktree       *bool   `json:"worktree"`
		Persona        *string `json:"persona"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if err := validEffort(a.ThinkingMode); err != nil {
		return nil, err
	}
	if err := m.agentOwned(ctx, c, a.AutomationID); err != nil {
		return nil, err
	}
	in := &automation.Input{Set: map[string]bool{}}
	setStr := func(key string, dst **string, v *string) {
		if v != nil {
			*dst = v
			in.Set[key] = true
		}
	}
	setStr("name", &in.Name, a.Name)
	setStr("prompt", &in.Prompt, a.Prompt)
	setStr("cron_expression", &in.CronExpression, a.CronExpression)
	setStr("timezone", &in.Timezone, a.Timezone)
	setStr("workspace_id", &in.WorkspaceID, a.WorkspaceID)
	setStr("thinking_mode", &in.ThinkingMode, a.ThinkingMode)
	setStr("selected_persona_name", &in.SelectedPersonaName, a.Persona)
	if a.ModelID != nil {
		model, err := resolveModel(a.ModelID)
		if err != nil {
			return nil, err
		}
		// Permission mode is left alone: a model switch must not lift a
		// user's plan/default-mode automation to bypassPermissions.
		setStr("model_id", &in.ModelID, &model)
	}
	if a.Worktree != nil {
		in.Worktree, in.Set["worktree"] = a.Worktree, true
	}
	if a.Enabled != nil {
		in.Enabled, in.Set["enabled"] = a.Enabled, true
	}
	x, err := m.automations.Update(ctx, a.AutomationID, c.userID, in, "")
	if err != nil {
		return nil, err
	}
	return map[string]any{"automation": automationSummary(x)}, nil
}

type automationIDArgs struct {
	AutomationID string `json:"automation_id"`
}

func (m *Module) deleteAutomation(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a automationIDArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if err := m.agentOwned(ctx, c, a.AutomationID); err != nil {
		return nil, err
	}
	if err := m.automations.Delete(ctx, a.AutomationID, c.userID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

func (m *Module) runAutomation(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a automationIDArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	chatID, err := m.automations.Run(context.WithoutCancel(ctx), a.AutomationID, c.userID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"chat_id": chatID}, nil
}
