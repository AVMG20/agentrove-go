package mcpserver

import (
	"context"
	"encoding/json"
	"strconv"

	"agentrove/internal/artifact"
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
			description: "List the available Bitbox workspaces.\n\nReturns each workspace's id and name. Pass a workspace id as `workspace_id` to " +
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
			description: "Replace an existing persona's system prompt.\n\n`name` must match a persona created with create_persona; " +
				"personas the user wrote can only be changed by the user. " +
				"`content` fully replaces its current system prompt. Chats already streaming keep the prompt they started with; " +
				"new turns pick up the updated one. Returns the updated persona's name.",
			schema:  object(props{"name": str(""), "content": str("")}, "name", "content"),
			handler: m.updatePersona,
		},
		{
			name: "delete_persona",
			description: "Delete a custom persona permanently.\n\n`name` must match a persona created with create_persona (the " +
				"user's own personas can't be deleted here). Chats " +
				"that used it keep their history, but new turns can no longer select it. Returns {\"deleted\": true}.",
			schema:  object(props{"name": str("")}, "name"),
			handler: m.deletePersona,
		},
		{
			name: "send_message",
			description: "Send a prompt to Bitbox and start the agent turn.\n\nOmit chat_id to create a new chat first " +
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
				"runs sandboxed with an opaque origin: cookies, localStorage, sessionStorage and Bitbox's API are unavailable, " +
				"so keep state that must survive a reload in Kit.store (see the artifacts skill). Make it responsive and readable on " +
				"its own; it is shown full-screen.\n\nArtifacts start " +
				"private: only the user, signed in, can open the link; they can make it public from the page. Returns the " +
				"artifact's id, url, version and visibility. Always give the user the url (as a markdown link) in your reply. To " +
				"revise it later, call edit_artifact (small changes) or update_artifact with its id instead of creating a new one. " +
				"The artifacts skill documents the styling kit (layout, charts, tables, diagrams, diffs, persistent state).",
			schema:  object(props{"title": str(""), "html": str("")}, "title", "html"),
			handler: m.createArtifact,
		},
		{
			name: "update_artifact", artifact: true,
			description: "Replace an existing artifact's HTML, and optionally its title.\n\n`html` is the complete new document, " +
				"not a diff; for targeted changes use edit_artifact instead. The link stays the same " +
				"and the version increments; open viewers pick up the new version on reload. Visibility is left unchanged. " +
				"Returns the updated artifact's id, url, version and visibility; share the url with the user again.",
			schema:  object(props{"artifact_id": str(""), "html": str(""), "title": optStr()}, "artifact_id", "html"),
			handler: m.updateArtifact,
		},
		{
			name: "edit_artifact", artifact: true,
			description: "Change part of an artifact's HTML with exact find-and-replace edits, without resending the page.\n\n" +
				"Edits apply in order, all or none. Each old_string must match the current HTML exactly once (include enough " +
				"surrounding text to make it unique) unless replace_all is set. Optionally renames the artifact. The version " +
				"increments like update_artifact. Returns the artifact's id, url, version and visibility.",
			schema: object(props{
				"artifact_id": str(""),
				"edits": map[string]any{"type": "array", "minItems": 1, "items": object(props{
					"old_string":  str("Exact text to replace"),
					"new_string":  str("Replacement text"),
					"replace_all": map[string]any{"type": "boolean", "default": false},
				}, "old_string", "new_string")},
				"title": optStr(),
			}, "artifact_id", "edits"),
			handler: m.editArtifact,
		},
		{
			name: "restore_artifact", artifact: true,
			description: "Roll an artifact back to an earlier version.\n\nPublishes that version's title and HTML as a new " +
				"version (history is kept; the last " + strconv.Itoa(artifact.KeepVersions) + " versions are available). " +
				"get_artifact lists the versions. Returns the artifact's id, url, version and visibility.",
			schema:  object(props{"artifact_id": str(""), "version": map[string]any{"type": "integer"}}, "artifact_id", "version"),
			handler: m.restoreArtifact,
		},
		{
			name: "list_artifacts", artifact: true,
			description: "List the user's artifacts, most recently updated first.\n\nWith this_chat, only those published from " +
				"the current chat. Returns each artifact's id, title, url, is_public, version and updated_at. Use an id with " +
				"get_artifact, edit_artifact or update_artifact.",
			schema:  object(props{"this_chat": map[string]any{"type": "boolean", "default": false}}),
			handler: m.listArtifacts,
		},
		{
			name: "get_artifact", artifact: true,
			description: "Get an artifact's metadata and its full HTML, by default the current version.\n\nAlso returns " +
				"`versions` (history, newest first) and `page_errors`: script errors, failed loads and console.error " +
				"output the user's browser hit on the current version. Check page_errors after the user has opened a " +
				"new version. Pass `version` to read an earlier one.",
			schema:  object(props{"artifact_id": str(""), "version": map[string]any{"type": []string{"integer", "null"}}}, "artifact_id"),
			handler: m.getArtifact,
		},
		{
			name: "get_artifact_state", artifact: true,
			description: "Read the state an artifact's page saved with Kit.store: a JSON object of key to value.\n\nUse it " +
				"to pick up what the user entered or checked off on the page (answers, selections, checklists).",
			schema:  object(props{"artifact_id": str("")}, "artifact_id"),
			handler: m.getArtifactState,
		},
		{
			name: "set_artifact_state", artifact: true,
			description: "Set one Kit.store key of an artifact's page; a null value deletes it.\n\nThe page sees the new " +
				"value on its next load. Values are any JSON up to " + strconv.Itoa(artifact.MaxStateValue/1000) + " KB.",
			schema:  object(props{"artifact_id": str(""), "key": str(""), "value": map[string]any{}}, "artifact_id", "key", "value"),
			handler: m.setArtifactState,
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
			description: "Read messages from an Bitbox chat; cursor pagination reaches the full history.\n\nEach call returns " +
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
				"Bitbox starts a new unattended chat and sends the prompt with bypassPermissions. cron_expression is a " +
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
			description: "Update an automation created with create_automation; only the arguments you pass are changed. " +
				"Automations the user created can only be changed by the user.\n\nPass model_id to switch " +
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
			name: "delete_automation",
			description: "Delete a scheduled automation created with create_automation permanently (the user's own automations " +
				"can't be deleted here). Returns {\"deleted\": true}.",
			schema:  object(props{"automation_id": str("")}, "automation_id"),
			handler: m.deleteAutomation,
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
