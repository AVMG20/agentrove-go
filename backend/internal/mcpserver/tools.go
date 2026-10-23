package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"agentrove/internal/app"
	"agentrove/internal/artifact"
	"agentrove/internal/automation"
	"agentrove/internal/channel"
	"agentrove/internal/db"
	"agentrove/internal/models"
)

type tool struct {
	name        string
	description string
	schema      map[string]any
	artifact    bool
	handler     func(ctx context.Context, c *caller, args json.RawMessage) (any, error)
}

// Schema helpers.
type props = map[string]any

func object(properties props, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any {
	s := map[string]any{"type": "string"}
	if desc != "" {
		s["description"] = desc
	}
	return s
}

func optStr() map[string]any         { return map[string]any{"type": []string{"string", "null"}} }
func boolean() map[string]any        { return map[string]any{"type": "boolean"} }
func optBool() map[string]any        { return map[string]any{"type": []string{"boolean", "null"}} }
func integer(def int) map[string]any { return map[string]any{"type": "integer", "default": def} }

func effort() map[string]any {
	return map[string]any{"type": []string{"string", "null"}, "enum": []any{"low", "medium", "high", "xhigh", "max", nil}}
}

// decode unmarshals tool arguments, reporting bad input to the model.
func decode(args json.RawMessage, v any) error {
	if err := json.Unmarshal(args, v); err != nil {
		return errorf("Invalid arguments: %v", err)
	}
	return nil
}

func validEffort(v *string) error {
	if v == nil || *v == "" {
		return nil
	}
	for _, e := range models.EffortLevels {
		if *v == e {
			return nil
		}
	}
	return errorf("thinking_mode must be one of low, medium, high, xhigh, max")
}

func (m *Module) buildTools() []*tool {
	return []*tool{
		{
			name: "list_workspaces",
			description: "List the available AgentRove workspaces.\n\nReturns each workspace's id and name. Pass a workspace id as `workspace_id` to " +
				"send_message to create a new chat in that workspace; otherwise the most recently active workspace is used.",
			schema:  object(props{}),
			handler: m.listWorkspaces,
		},
		{
			name: "list_models",
			description: "List available AI models.\n\nEach entry has model_id, name, agent_kind and thinking_modes — the reasoning-effort " +
				"tiers the model accepts for send_message's thinking_mode, ordered lowest to highest. Pass a model_id as `model_id` " +
				"to send_message to use it; otherwise the default Claude model is used.",
			schema:  object(props{"agent_kind": map[string]any{"type": []string{"string", "null"}, "enum": []any{"claude", nil}}}),
			handler: m.listModels,
		},
		{
			name: "get_current_chat",
			description: "Return the chat this MCP server is running inside, when known.\n\nUse its chat_id as send_message's " +
				"parent_chat_id to create sub-threads under the current chat. Returns {\"chat_id\": null} if unknown.",
			schema: object(props{}),
			handler: func(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
				var id any
				if c.chatID != "" {
					id = c.chatID
				}
				return map[string]any{"chat_id": id}, nil
			},
		},
		{
			name: "list_chats",
			description: "List existing chats for discovery, most recent first.\n\nUse a chat's id with send_message — as chat_id to " +
				"continue it, or as parent_chat_id to add a sub-thread (only top-level chats, where parent_chat_id is null, can be " +
				"parents). Filter by workspace_id to scope to one workspace. Returns id, title, workspace_id, parent_chat_id, " +
				"sub_thread_count and updated_at, plus pagination.",
			schema:  object(props{"workspace_id": optStr(), "page": integer(1), "per_page": integer(20)}),
			handler: m.listChats,
		},
		{
			name: "list_personas",
			description: "List custom personas available for send_message's `persona` argument.\n\nReturns each persona's name and " +
				"system prompt content. Pass a name as `persona` to send_message; if none are defined or `persona` is omitted, " +
				"the standard \"Default\" persona is used.",
			schema:  object(props{}),
			handler: m.listPersonas,
		},
		{
			name: "get_persona",
			description: "Get a persona's name and system prompt content.\n\n`name` must match an existing persona (see " +
				"list_personas). Returns name and content so you can inspect or copy a persona before updating it or using it " +
				"with send_message.",
			schema:  object(props{"name": str("")}, "name"),
			handler: m.getPersona,
		},
		{
			name: "create_persona",
			description: "Create a custom persona usable as send_message's `persona` argument.\n\n`name` is the unique label you'll " +
				"pass as `persona` (it must not match an existing persona); `content` is the system prompt that shapes the " +
				"agent's behavior for turns using this persona. Returns the created persona's name.",
			schema:  object(props{"name": str(""), "content": str("")}, "name", "content"),
			handler: m.createPersona,
		},
		{
			name: "update_persona",
			description: "Replace an existing persona's system prompt.\n\n`name` must match an existing persona (see list_personas); " +
				"`content` fully replaces its current system prompt. Chats already streaming keep the prompt they started with; " +
				"new turns pick up the updated one. Returns the updated persona's name.",
			schema:  object(props{"name": str(""), "content": str("")}, "name", "content"),
			handler: m.updatePersona,
		},
		{
			name: "delete_persona",
			description: "Delete a custom persona permanently.\n\n`name` must match an existing persona (see list_personas). Chats " +
				"that used it keep their history, but new turns can no longer select it. Returns {\"deleted\": true}.",
			schema:  object(props{"name": str("")}, "name"),
			handler: m.deletePersona,
		},
		{
			name: "send_message",
			description: "Send a prompt to AgentRove and start the agent turn.\n\nOmit chat_id to create a new chat first " +
				"(auto-resolving the workspace and a Claude model unless given, titled from `title` or the prompt) and send the " +
				"prompt to it; pass chat_id to continue an existing chat. Set parent_chat_id (when creating) to make the new chat a " +
				"sub-thread of that parent — it inherits the parent's workspace, and the parent must be a top-level chat " +
				"(sub-threads can't nest).\n\nPer-turn options: thinking_mode sets reasoning effort; worktree=true runs the turn " +
				"in an isolated git worktree (its own branch); persona selects a custom persona by name (defaults to the standard " +
				"persona).\nbase_branch: start the worktree from this branch instead of current HEAD; only used with " +
				"worktree=true.\n\nFollow-up turns (chat_id given) inherit the chat's previous model_id, thinking_mode and persona " +
				"when you omit them — pass a value only to change it for this turn.\n\nReturns immediately with chat_id and the " +
				"streaming message_id. To get the reply, poll get_messages and watch that message's stream_status flip from " +
				"\"in_progress\" to \"completed\". The turn runs unattended with bypassPermissions.",
			schema: object(props{
				"prompt": str(""), "chat_id": optStr(), "parent_chat_id": optStr(), "title": optStr(), "workspace_id": optStr(),
				"model_id": optStr(), "thinking_mode": effort(), "worktree": boolean(), "base_branch": optStr(), "persona": optStr(),
			}, "prompt"),
			handler: m.sendMessage,
		},
		{
			name: "render_ui",
			description: "Render an interactive UI widget (calculator, form, chart, simulation, ...) in the chat.\n\n`html` is a " +
				"self-contained HTML fragment with inline <style> and <script> only. It runs in an isolated sandbox: no network, " +
				"CDNs, fetch, cookies, localStorage, or parent-document access; images and fonts must be data: or blob: URLs. The " +
				"host styles plain button/input/select elements and provides the CSS variables --color-text-primary, " +
				"--color-background-secondary, --color-border-tertiary, --font-sans and --border-radius-md. Keep calculations, " +
				"filters and other local interactions inside the widget. On an explicit user action, call " +
				"window.agentrove.sendMessage(text) to put text in the user's composer — it is not sent until the user presses " +
				"Send, so never call it on load. Form submit handlers must call preventDefault(); form actions are blocked. Do not " +
				"implement the MCP Apps handshake yourself; the host supplies it. The widget is rendered from this call's " +
				"arguments once the call completes (it does not stream), so the result carries nothing to act on. Returns " +
				"{\"rendered\": true, \"title\": title}.",
			schema: object(props{"title": str(""), "html": str("")}, "title", "html"),
			handler: func(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
				var a struct{ Title string }
				if err := decode(args, &a); err != nil {
					return nil, err
				}
				return map[string]any{"rendered": true, "title": a.Title}, nil
			},
		},
		{
			name: "create_artifact", artifact: true,
			description: "Publish a self-contained HTML page as an artifact with its own shareable link.\n\nUse an artifact when " +
				"the user wants something to open, keep or share as a page: a report, dashboard, chart, prototype, slide deck, " +
				"game or document. Prefer it over writing an .html file into the workspace for anything meant to be viewed; for a " +
				"small widget that belongs inline in the chat, use render_ui when available.\n\n`html` is a complete single-file " +
				"document (<!doctype html>...) with inline <style> and <script>. Scripts, stylesheets and fonts from public CDNs " +
				"(cdn.jsdelivr.net, unpkg.com, cdnjs.cloudflare.com, Google Fonts) and fetch() to public CORS APIs work. The page " +
				"runs sandboxed with an opaque origin: cookies, localStorage, sessionStorage and AgentRove's API are unavailable, " +
				"so keep state in memory. Make it responsive and readable on its own; it is shown full-screen.\n\nArtifacts start " +
				"private: only the user, signed in, can open the link; they can make it public from the page. Returns the " +
				"artifact's id, url, version and visibility. Always give the user the url (as a markdown link) in your reply. To " +
				"revise it later, call update_artifact with its id instead of creating a new one.",
			schema:  object(props{"title": str(""), "html": str("")}, "title", "html"),
			handler: m.createArtifact,
		},
		{
			name: "update_artifact", artifact: true,
			description: "Replace an existing artifact's HTML, and optionally its title.\n\n`html` is the complete new document, " +
				"not a diff (read the current one with get_artifact first when making targeted edits). The link stays the same " +
				"and the version increments; open viewers pick up the new version on reload. Visibility is left unchanged. " +
				"Returns the updated artifact's id, url, version and visibility; share the url with the user again.",
			schema:  object(props{"artifact_id": str(""), "html": str(""), "title": optStr()}, "artifact_id", "html"),
			handler: m.updateArtifact,
		},
		{
			name: "list_artifacts", artifact: true,
			description: "List the user's artifacts, most recently updated first.\n\nReturns each artifact's id, title, url, " +
				"is_public, version and updated_at. Use an id with get_artifact or update_artifact.",
			schema:  object(props{}),
			handler: m.listArtifacts,
		},
		{
			name: "get_artifact", artifact: true,
			description: "Get an artifact's metadata and its full current HTML.\n\nUse it to read the page before revising it " +
				"with update_artifact. Returns id, title, url, is_public, version, updated_at and html.",
			schema:  object(props{"artifact_id": str("")}, "artifact_id"),
			handler: m.getArtifact,
		},
		{
			name: "stop_chat",
			description: "Stop the in-progress agent turn in a chat, e.g. a sub-thread started with send_message.\n\nCancels the " +
				"running turn and waits briefly for it to unwind, so a follow-up send_message to the same chat can start right " +
				"away. The partial reply is kept with stream_status \"interrupted\" (see get_messages). Stopping an idle chat is a " +
				"no-op. The chat this server runs inside (get_current_chat) can't be stopped — that would cancel the turn making " +
				"this call. Returns {\"stopped\": true}.",
			schema:  object(props{"chat_id": str("")}, "chat_id"),
			handler: m.stopChat,
		},
		{
			name: "get_messages",
			description: "Read messages from an AgentRove chat; cursor pagination reaches the full history.\n\nEach call returns " +
				"up to `limit` messages (1-100) with role, content_text and stream_status, ordered oldest-to-newest within the " +
				"page. The first call (no cursor) returns the MOST RECENT messages; pass the returned next_cursor to fetch the " +
				"preceding, older page. Repeat while has_more is true to read the entire chat — no part of the history is " +
				"unreachable.\n\nAlso returns context_usage — {tokens_used, context_window, percentage} for the chat's agent " +
				"session. A high percentage means follow-ups in this chat run with little headroom; prefer a fresh chat or " +
				"sub-thread with a distilled handoff instead of continuing a nearly-full one.",
			schema:  object(props{"chat_id": str(""), "limit": integer(20), "cursor": optStr()}, "chat_id"),
			handler: m.getMessages,
		},
		{
			name: "list_channels",
			description: "List multi-agent channels, optionally filtered by workspace_id.\n\nReturns channel settings and members, " +
				"including each member's chat_id. Use a channel's id with post_channel_message to continue its discussion and " +
				"with get_channel_messages to read replies. Omit workspace_id to list channels across all workspaces.",
			schema:  object(props{"workspace_id": optStr()}),
			handler: m.listChannels,
		},
		{
			name: "create_channel",
			description: "Create a multi-agent channel for a discussion or debate.\n\nSupply at least one member, each with a " +
				"model_id from list_models and optional display_name, persona (a name from list_personas), and thinking_mode (as " +
				"in send_message). Display names must match [A-Za-z0-9][A-Za-z0-9._-]{0,31}. Members run unattended with " +
				"bypassPermissions. Omit workspace_id to use the most recently active workspace. Set worktree=true for an isolated " +
				"git worktree. branch selects the checkout or worktree base branch.\n\nReturns the channel summary, including its " +
				"id and each member's chat_id. Creating the channel does not start the discussion: call post_channel_message with " +
				"its id. Members reply asynchronously; read the discussion with get_channel_messages.",
			schema: object(props{
				"name": str(""),
				"members": map[string]any{"type": "array", "minItems": 1, "items": object(props{
					"model_id":      str(""),
					"display_name":  map[string]any{"type": []string{"string", "null"}, "pattern": "^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$"},
					"persona":       optStr(),
					"thinking_mode": effort(),
				}, "model_id")},
				"workspace_id": optStr(), "worktree": boolean(), "branch": optStr(),
			}, "name", "members"),
			handler: m.createChannel,
		},
		{
			name: "post_channel_message",
			description: "Post a message as the user to a channel, triggering its members' replies.\n\nMembers reply " +
				"asynchronously; use get_channel_messages with the channel_id to read the discussion and members' replies.\n" +
				"Returns the posted message_id, channel_id, seq and status, not the members' replies.",
			schema:  object(props{"channel_id": str(""), "content": str("")}, "channel_id", "content"),
			handler: m.postChannelMessage,
		},
		{
			name: "get_channel_messages",
			description: "Read a channel's conversation oldest-first, ordered by seq.\n\nPass the highest seq seen as after_seq to " +
				"get only newer messages. Each message includes its author's display name (or \"user\"), member_id, content and " +
				"status. Status is \"streaming\" while a member is replying and content is partial, then \"completed\" when done; " +
				"\"cancelled\" and \"deleted\" also occur.\n\nUse this to follow a debate in one call instead of reading each " +
				"member's chat.",
			schema:  object(props{"channel_id": str(""), "after_seq": integer(0)}, "channel_id"),
			handler: m.getChannelMessages,
		},
		{
			name: "stop_channel",
			description: "Stop a channel's members' in-progress replies and drop queued, undelivered messages.\n\nPartial replies " +
				"stay with status \"cancelled\" in get_channel_messages. Members won't answer the queued messages. The channel " +
				"stays open; post_channel_message resumes the discussion. Stopping an idle channel is a no-op. A chat cannot stop " +
				"a channel it belongs to from within its own turn, since that would cancel the calling turn. Returns " +
				"{\"stopped\": true}.",
			schema:  object(props{"channel_id": str("")}, "channel_id"),
			handler: m.stopChannel,
		},
		{
			name: "list_automations",
			description: "List your scheduled automations.\n\nEach automation runs its prompt on a cron schedule, starting a new " +
				"unattended chat every time it fires. Returns id, name, cron_expression, timezone, enabled, model_id, " +
				"workspace_id and the next_run_at / last_run_at timestamps. Use an id with update_automation, delete_automation " +
				"or run_automation.",
			schema:  object(props{}),
			handler: m.listAutomations,
		},
		{
			name: "create_automation",
			description: "Create a scheduled automation that runs a prompt on a cron schedule.\n\nEach time the schedule fires, " +
				"AgentRove starts a new unattended chat and sends the prompt with bypassPermissions. cron_expression is a " +
				"standard 5-field cron string evaluated in `timezone` (an IANA name like \"America/New_York\"). Omit model_id to " +
				"use the default Claude model and workspace_id to use the most recently active workspace. Per-run options " +
				"(thinking_mode, worktree, persona) match send_message. Set enabled=false to create it paused.\n\nReturns the " +
				"created automation, including its id and next_run_at.",
			schema: object(props{
				"name": str(""), "prompt": str(""), "cron_expression": str(""),
				"timezone":     map[string]any{"type": "string", "default": "UTC"},
				"workspace_id": optStr(), "model_id": optStr(), "thinking_mode": effort(), "worktree": boolean(),
				"persona": optStr(), "enabled": map[string]any{"type": "boolean", "default": true},
			}, "name", "prompt", "cron_expression"),
			handler: m.createAutomation,
		},
		{
			name: "update_automation",
			description: "Update an existing automation; only the arguments you pass are changed.\n\nPass model_id to switch " +
				"models, enabled to pause/resume, or cron_expression/timezone to reschedule (next_run_at is recomputed). Returns " +
				"the updated automation.",
			schema: object(props{
				"automation_id": str(""), "name": optStr(), "prompt": optStr(), "cron_expression": optStr(), "timezone": optStr(),
				"workspace_id": optStr(), "model_id": optStr(), "thinking_mode": effort(), "worktree": optBool(),
				"persona": optStr(), "enabled": optBool(),
			}, "automation_id"),
			handler: m.updateAutomation,
		},
		{
			name:        "delete_automation",
			description: "Delete a scheduled automation permanently. Returns {\"deleted\": true}.",
			schema:      object(props{"automation_id": str("")}, "automation_id"),
			handler:     m.deleteAutomation,
		},
		{
			name: "run_automation",
			description: "Trigger an automation immediately, without waiting for its schedule.\n\nStarts a new unattended chat " +
				"with the automation's prompt right now; the regular cron schedule (next_run_at) is unaffected. Returns the " +
				"chat_id of the new chat — poll get_messages on it to read the reply.",
			schema:  object(props{"automation_id": str("")}, "automation_id"),
			handler: m.runAutomation,
		},
	}
}

// --- workspaces, models, chats ---

func (m *Module) listWorkspaces(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	ws, err := m.workspaces(ctx, c.userID, 100)
	if err != nil {
		return nil, err
	}
	return map[string]any{"workspaces": ws}, nil
}

// workspaces lists the user's workspaces, most recently active first.
func (m *Module) workspaces(ctx context.Context, userID string, limit int) ([]map[string]any, error) {
	rows, err := m.a.DB.QueryContext(ctx, `SELECT w.id, w.name FROM workspaces w
		LEFT JOIN chats c ON c.workspace_id = w.id AND c.deleted_at IS NULL AND c.channel_id IS NULL AND c.parent_chat_id IS NULL
		WHERE w.user_id = ? AND w.deleted_at IS NULL
		GROUP BY w.id ORDER BY MAX(c.updated_at) IS NULL, MAX(c.updated_at) DESC, w.updated_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"id": id, "name": name})
	}
	return out, rows.Err()
}

func (m *Module) resolveWorkspace(ctx context.Context, userID string, id *string) (string, error) {
	if id != nil && *id != "" {
		return *id, nil
	}
	ws, err := m.workspaces(ctx, userID, 1)
	if err != nil {
		return "", err
	}
	if len(ws) == 0 {
		return "", errorf("No workspaces found — create one in AgentRove first.")
	}
	return ws[0]["id"].(string), nil
}

func (m *Module) listModels(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		AgentKind *string `json:"agent_kind"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list := []map[string]any{}
	if a.AgentKind == nil || *a.AgentKind == "" || *a.AgentKind == "claude" {
		for _, mi := range models.ClaudeModels {
			list = append(list, map[string]any{"model_id": mi.ID, "name": mi.Name, "agent_kind": mi.AgentKind, "thinking_modes": mi.EffortLevels})
		}
	}
	return map[string]any{"models": list}, nil
}

func (m *Module) listChats(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	a := struct {
		WorkspaceID *string `json:"workspace_id"`
		Page        int     `json:"page"`
		PerPage     int     `json:"per_page"`
	}{Page: 1, PerPage: 20}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	a.Page = max(a.Page, 1)
	a.PerPage = min(max(a.PerPage, 1), 100)
	where := `c.user_id = ? AND c.deleted_at IS NULL AND c.channel_id IS NULL AND c.parent_chat_id IS NULL`
	params := []any{c.userID}
	if a.WorkspaceID != nil && *a.WorkspaceID != "" {
		where += ` AND c.workspace_id = ?`
		params = append(params, *a.WorkspaceID)
	}
	var total int
	if err := m.a.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM chats c WHERE `+where, params...).Scan(&total); err != nil {
		return nil, err
	}
	rows, err := m.a.DB.QueryContext(ctx, `SELECT c.id, c.title, c.workspace_id, c.parent_chat_id, c.updated_at,
		(SELECT COUNT(1) FROM chats s WHERE s.parent_chat_id = c.id AND s.user_id = c.user_id AND s.deleted_at IS NULL AND s.channel_id IS NULL)
		FROM chats c WHERE `+where+` ORDER BY c.pinned_at IS NULL, c.pinned_at DESC, c.updated_at DESC LIMIT ? OFFSET ?`,
		append(params, a.PerPage, (a.Page-1)*a.PerPage)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := []map[string]any{}
	for rows.Next() {
		var id, title, ws string
		var parent *string
		var updated db.Time
		var subs int
		if err := rows.Scan(&id, &title, &ws, &parent, &updated, &subs); err != nil {
			return nil, err
		}
		chats = append(chats, map[string]any{"id": id, "title": title, "workspace_id": ws, "parent_chat_id": parent, "sub_thread_count": subs, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	pages := (total + a.PerPage - 1) / a.PerPage
	return map[string]any{"chats": chats, "page": a.Page, "pages": pages, "total": total}, nil
}

// --- personas (a list in user settings) ---

func (m *Module) personas(ctx context.Context, userID string) ([]models.Persona, error) {
	st, err := m.a.Store.GetUserSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !st.Personas.Valid || st.Personas.V == nil {
		return []models.Persona{}, nil
	}
	return st.Personas.V, nil
}

func (m *Module) savePersonas(ctx context.Context, userID string, list []models.Persona) error {
	_, err := m.a.DB.ExecContext(ctx, `UPDATE user_settings SET personas = ?, updated_at = ? WHERE user_id = ?`, db.J(list), db.Now(), userID)
	return err
}

func (m *Module) listPersonas(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, p := range list {
		out = append(out, map[string]any{"name": p.Name, "content": p.Content})
	}
	return map[string]any{"personas": out}, nil
}

type personaArgs struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

func (m *Module) getPersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Name == a.Name {
			return map[string]any{"persona": map[string]any{"name": p.Name, "content": p.Content}}, nil
		}
	}
	return nil, errorf("No persona named %q exists.", a.Name)
}

func (m *Module) createPersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Name) == "" {
		return nil, errorf("name is required")
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if p.Name == a.Name {
			return nil, errorf("A persona named %q already exists.", a.Name)
		}
	}
	if err := m.savePersonas(ctx, c.userID, append(list, models.Persona{Name: a.Name, Content: a.Content})); err != nil {
		return nil, err
	}
	return map[string]any{"persona": map[string]any{"name": a.Name}}, nil
}

func (m *Module) updatePersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == a.Name {
			list[i].Content = a.Content
			if err := m.savePersonas(ctx, c.userID, list); err != nil {
				return nil, err
			}
			return map[string]any{"persona": map[string]any{"name": a.Name}}, nil
		}
	}
	return nil, errorf("No persona named %q exists.", a.Name)
}

func (m *Module) deletePersona(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a personaArgs
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	list, err := m.personas(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	kept := []models.Persona{}
	for _, p := range list {
		if p.Name != a.Name {
			kept = append(kept, p)
		}
	}
	if len(kept) == len(list) {
		return nil, errorf("No persona named %q exists.", a.Name)
	}
	if err := m.savePersonas(ctx, c.userID, kept); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// --- chat turns ---

func (m *Module) sendMessage(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Prompt       string  `json:"prompt"`
		ChatID       *string `json:"chat_id"`
		ParentChatID *string `json:"parent_chat_id"`
		Title        *string `json:"title"`
		WorkspaceID  *string `json:"workspace_id"`
		ModelID      *string `json:"model_id"`
		ThinkingMode *string `json:"thinking_mode"`
		Worktree     bool    `json:"worktree"`
		BaseBranch   *string `json:"base_branch"`
		Persona      *string `json:"persona"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(a.Prompt); n < 1 || n > 100000 {
		return nil, errorf("prompt must be between 1 and 100000 characters")
	}
	if err := validEffort(a.ThinkingMode); err != nil {
		return nil, err
	}
	start := m.a.Hooks.StartChatTurn
	if start == nil {
		return nil, errorf("Chat runtime unavailable")
	}
	chatID := ""
	if a.ChatID != nil && *a.ChatID != "" {
		chat, err := m.a.Store.GetChat(ctx, c.userID, *a.ChatID)
		if err != nil {
			return nil, err
		}
		chatID = chat.ID
		// Inherit the last turn's settings when omitted.
		if a.ModelID == nil || *a.ModelID == "" {
			a.ModelID = chat.LastModelID
		}
		if a.ThinkingMode == nil || *a.ThinkingMode == "" {
			a.ThinkingMode = chat.LastThinkingMode
		}
		if a.Persona == nil || *a.Persona == "" {
			a.Persona = chat.LastPersonaName
		}
	}
	model, err := resolveModel(a.ModelID)
	if err != nil {
		return nil, err
	}
	created := false
	if chatID == "" {
		ws, err := m.resolveWorkspace(ctx, c.userID, a.WorkspaceID)
		if err != nil {
			return nil, err
		}
		title := a.Prompt
		if a.Title != nil && *a.Title != "" {
			title = *a.Title
		} else if r := []rune(title); len(r) > 60 {
			title = string(r[:60])
		}
		title = strings.TrimSpace(title)
		if title == "" {
			title = "New chat"
		}
		var parent *string
		if a.ParentChatID != nil && *a.ParentChatID != "" {
			parent = a.ParentChatID
		}
		chat, err := automation.CreateChat(ctx, m.a, c.userID, ws, title, parent)
		if err != nil {
			return nil, err
		}
		chatID, created = chat.ID, true
	}
	persona := models.DefaultPersonaName
	if a.Persona != nil && *a.Persona != "" {
		persona = *a.Persona
	}
	var thinking *string
	if a.ThinkingMode != nil && *a.ThinkingMode != "" {
		thinking = a.ThinkingMode
	}
	var base *string
	if a.BaseBranch != nil && *a.BaseBranch != "" {
		base = a.BaseBranch
	}
	res, err := start(app.ChatTurnRequest{
		UserID: c.userID, ChatID: chatID, Prompt: a.Prompt, ModelID: model, PermissionMode: models.FullAccessPermissionMode,
		ThinkingMode: thinking, Worktree: a.Worktree, BaseBranch: base, SelectedPersonaName: persona, Unattended: true,
	})
	if err != nil {
		if created {
			automation.DeleteChat(context.WithoutCancel(ctx), m.a, chatID)
		}
		return nil, err
	}
	return map[string]any{"chat_id": chatID, "message_id": res.MessageID, "status": "in_progress"}, nil
}

func (m *Module) stopChat(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChatID string `json:"chat_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if c.chatID != "" && a.ChatID == c.chatID {
		return nil, errorf("Cannot stop the current chat from within its own turn.")
	}
	chat, err := m.a.Store.GetChat(ctx, c.userID, a.ChatID)
	if err != nil {
		return nil, err
	}
	if cancel := m.a.Hooks.CancelChatTurn; cancel != nil {
		if err := cancel(chat.ID); err != nil {
			return nil, err
		}
	}
	return map[string]any{"stopped": true}, nil
}

func encodeCursor(created db.Time, id string) string {
	b, _ := json.Marshal([]string{created.UTC().Format(time.RFC3339Nano), id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeCursor(cur string) (db.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cur)
	if err != nil {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	var parts []string
	if err := json.Unmarshal(raw, &parts); err != nil || len(parts) != 2 {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return db.Time{}, "", errorf("Invalid cursor")
	}
	return db.T(t), parts[1], nil
}

func (m *Module) getMessages(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	a := struct {
		ChatID string  `json:"chat_id"`
		Limit  int     `json:"limit"`
		Cursor *string `json:"cursor"`
	}{Limit: 20}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	a.Limit = min(max(a.Limit, 1), 100)
	chat, err := m.a.Store.GetMemberChat(ctx, c.userID, a.ChatID)
	if err != nil {
		return nil, err
	}
	q := `SELECT id, role, content_text, stream_status, created_at FROM messages WHERE chat_id = ? AND deleted_at IS NULL`
	params := []any{chat.ID}
	if a.Cursor != nil && *a.Cursor != "" {
		t, id, err := decodeCursor(*a.Cursor)
		if err != nil {
			return nil, err
		}
		q += ` AND (created_at < ? OR (created_at = ? AND id < ?))`
		params = append(params, t, t, id)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	params = append(params, a.Limit+1)
	rows, err := m.a.DB.QueryContext(ctx, q, params...)
	if err != nil {
		return nil, err
	}
	type row struct {
		id, role, content, status string
		created                   db.Time
	}
	var page []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.role, &r.content, &r.status, &r.created); err != nil {
			rows.Close()
			return nil, err
		}
		page = append(page, r)
	}
	rows.Close()
	hasMore := len(page) > a.Limit
	if hasMore {
		page = page[:a.Limit]
	}
	var next any
	if hasMore && len(page) > 0 {
		last := page[len(page)-1]
		next = encodeCursor(last.created, last.id)
	}
	// Newest-first from the DB; reverse for chronological reading.
	msgs := make([]map[string]any, 0, len(page))
	for i := len(page) - 1; i >= 0; i-- {
		r := page[i]
		msgs = append(msgs, map[string]any{"id": r.id, "role": r.role, "content": r.content, "stream_status": r.status, "created_at": r.created})
	}
	return map[string]any{"messages": msgs, "next_cursor": next, "has_more": hasMore, "context_usage": contextUsage(chat.ContextTokenUsage, chat.ContextWindow, chat.LastModelID)}, nil
}

func contextUsage(used, window int, modelID *string) map[string]any {
	if window <= 0 && modelID != nil {
		if mi, ok := models.LookupModel(*modelID); ok {
			window = mi.ContextWindow
		}
	}
	pct := 0.0
	if window > 0 {
		pct = min(float64(used)/float64(window)*100, 100)
	}
	return map[string]any{"tokens_used": used, "context_window": window, "percentage": pct}
}

// --- channels ---

func channelSummary(ch *channel.Channel) map[string]any {
	members := []map[string]any{}
	for _, mb := range ch.Members {
		members = append(members, map[string]any{
			"id": mb.ID, "chat_id": mb.ChatID, "display_name": mb.DisplayName, "model_id": mb.ModelID,
			"persona": mb.Persona, "thinking_mode": mb.ThinkingMode,
		})
	}
	return map[string]any{
		"id": ch.ID, "name": ch.Name, "workspace_id": ch.WorkspaceID, "worktree": ch.Worktree, "branch": ch.Branch,
		"created_at": ch.CreatedAt, "updated_at": ch.UpdatedAt, "members": members,
	}
}

func (m *Module) listChannels(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		WorkspaceID *string `json:"workspace_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	chans, err := m.channels.List(ctx, c.userID, db.Deref(a.WorkspaceID))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, ch := range chans {
		out = append(out, channelSummary(ch))
	}
	return map[string]any{"channels": out}, nil
}

func (m *Module) createChannel(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Name    string `json:"name"`
		Members []struct {
			ModelID      string  `json:"model_id"`
			DisplayName  *string `json:"display_name"`
			Persona      *string `json:"persona"`
			ThinkingMode *string `json:"thinking_mode"`
		} `json:"members"`
		WorkspaceID *string `json:"workspace_id"`
		Worktree    bool    `json:"worktree"`
		Branch      *string `json:"branch"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if len(a.Members) == 0 {
		return nil, errorf("members must have at least one item")
	}
	ws, err := m.resolveWorkspace(ctx, c.userID, a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	in := channel.CreateInput{WorkspaceID: ws, Name: a.Name, Worktree: a.Worktree, Branch: a.Branch}
	for _, mb := range a.Members {
		model, err := resolveModel(&mb.ModelID)
		if err != nil {
			return nil, err
		}
		if err := validEffort(mb.ThinkingMode); err != nil {
			return nil, err
		}
		mode := models.FullAccessPermissionMode
		in.Members = append(in.Members, channel.MemberInput{
			ModelID: model, DisplayName: mb.DisplayName, PermissionMode: &mode, Persona: mb.Persona, ThinkingMode: mb.ThinkingMode,
		})
	}
	ch, err := m.channels.Create(ctx, c.userID, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channel": channelSummary(ch)}, nil
}

func (m *Module) postChannelMessage(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
		Content   string `json:"content"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if n := utf8.RuneCountInString(a.Content); n < 1 || n > 100000 {
		return nil, errorf("content must be between 1 and 100000 characters")
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	msg, err := m.channels.Post(context.WithoutCancel(ctx), ch, a.Content, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"message_id": msg.ID, "channel_id": msg.ChannelID, "seq": msg.Seq, "status": msg.Status}, nil
}

func (m *Module) getChannelMessages(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
		AfterSeq  int64  `json:"after_seq"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	msgs, err := m.channels.Messages(ctx, ch.ID, max(a.AfterSeq, 0))
	if err != nil {
		return nil, err
	}
	authors := map[string]string{}
	for _, mb := range ch.Members {
		authors[mb.ID] = mb.DisplayName
	}
	out := []map[string]any{}
	for _, msg := range msgs {
		author := "user"
		if msg.MemberID != nil {
			author = authors[*msg.MemberID]
		}
		out = append(out, map[string]any{
			"id": msg.ID, "seq": msg.Seq, "author": author, "member_id": msg.MemberID, "content": msg.Content,
			"status": msg.Status, "created_at": msg.CreatedAt,
		})
	}
	return map[string]any{"messages": out}, nil
}

func (m *Module) stopChannel(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ChannelID string `json:"channel_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	ch, err := m.channels.Get(ctx, a.ChannelID, c.userID)
	if err != nil {
		return nil, err
	}
	for _, mb := range ch.Members {
		if c.chatID != "" && mb.ChatID == c.chatID {
			return nil, errorf("Cannot stop a channel this chat is a member of from within its own turn.")
		}
	}
	m.channels.Stop(context.WithoutCancel(ctx), ch)
	return map[string]any{"stopped": true}, nil
}

// --- automations ---

func automationSummary(x *automation.Automation) map[string]any {
	return map[string]any{
		"id": x.ID, "name": x.Name, "prompt": x.Prompt, "cron_expression": x.CronExpression, "timezone": x.Timezone,
		"enabled": x.Enabled, "model_id": x.ModelID, "workspace_id": x.WorkspaceID, "thinking_mode": x.ThinkingMode,
		"worktree": x.Worktree, "persona": x.SelectedPersonaName, "next_run_at": x.NextRunAt, "last_run_at": x.LastRunAt,
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
		mode := models.FullAccessPermissionMode
		setStr("model_id", &in.ModelID, &model)
		setStr("permission_mode", &in.PermissionMode, &mode)
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

// --- artifacts ---

func (m *Module) artifactSummary(x *artifact.Artifact) map[string]any {
	return map[string]any{
		"id": x.ID, "title": x.Title, "url": strings.TrimRight(m.a.Cfg.FrontendURL, "/") + "/artifacts/" + x.ID,
		"is_public": x.IsPublic, "version": x.Version, "updated_at": x.UpdatedAt,
	}
}

func (m *Module) createArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		Title string `json:"title"`
		HTML  string `json:"html"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	var chatID *string
	if c.chatID != "" {
		chatID = &c.chatID
	}
	x, err := m.artifacts.Create(ctx, c.userID, a.Title, a.HTML, chatID)
	if errors.Is(err, artifact.ErrChatNotFound) {
		// A chat id the user doesn't own: publish unlinked rather than fail.
		x, err = m.artifacts.Create(ctx, c.userID, a.Title, a.HTML, nil)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) updateArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string  `json:"artifact_id"`
		HTML       string  `json:"html"`
		Title      *string `json:"title"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	if a.Title != nil && *a.Title == "" {
		a.Title = nil
	}
	x, err := m.artifacts.Update(ctx, a.ArtifactID, c.userID, a.Title, &a.HTML, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"artifact": m.artifactSummary(x)}, nil
}

func (m *Module) listArtifacts(ctx context.Context, c *caller, _ json.RawMessage) (any, error) {
	list, err := m.artifacts.List(ctx, c.userID)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, x := range list {
		out = append(out, m.artifactSummary(x))
	}
	return map[string]any{"artifacts": out}, nil
}

func (m *Module) getArtifact(ctx context.Context, c *caller, args json.RawMessage) (any, error) {
	var a struct {
		ArtifactID string `json:"artifact_id"`
	}
	if err := decode(args, &a); err != nil {
		return nil, err
	}
	x, err := m.artifacts.Get(ctx, a.ArtifactID, c.userID)
	if err != nil {
		return nil, err
	}
	s := m.artifactSummary(x)
	s["html"] = x.HTML
	return map[string]any{"artifact": s}, nil
}
