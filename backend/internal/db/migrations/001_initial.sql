-- Timestamps are RFC3339 UTC text (see db.Time); ids are canonical UUID text.

CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    username TEXT NOT NULL UNIQUE,
    hashed_password TEXT NOT NULL,
    is_active INTEGER NOT NULL DEFAULT 1,
    is_superuser INTEGER NOT NULL DEFAULT 0,
    is_verified INTEGER NOT NULL DEFAULT 0,
    verification_token TEXT,
    verification_token_expires TEXT,
    reset_token TEXT,
    reset_token_expires TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE user_settings (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    github_personal_access_token TEXT,
    custom_instructions TEXT,
    custom_env_vars TEXT,
    personas TEXT,
    stream_actions TEXT,
    notifications_enabled INTEGER NOT NULL DEFAULT 1,
    title_model_id TEXT NOT NULL DEFAULT 'haiku',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE refresh_tokens (
    id TEXT PRIMARY KEY,
    token_hash TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL,
    revoked_at TEXT,
    user_agent TEXT,
    ip_address TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_refresh_tokens_hash ON refresh_tokens(token_hash);
CREATE INDEX idx_refresh_tokens_user_revoked ON refresh_tokens(user_id, revoked_at);

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    sandbox_id TEXT NOT NULL,
    sandbox_provider TEXT NOT NULL DEFAULT 'docker',
    workspace_path TEXT NOT NULL,
    source_type TEXT,
    source_url TEXT,
    deleted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_workspaces_user_deleted ON workspaces(user_id, deleted_at);

CREATE TABLE channels (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    worktree INTEGER NOT NULL DEFAULT 0,
    branch TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_channels_user ON channels(user_id);
CREATE INDEX idx_channels_workspace ON channels(workspace_id);

CREATE TABLE chats (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    channel_id TEXT REFERENCES channels(id) ON DELETE CASCADE,
    session_id TEXT,
    worktree_cwd TEXT,
    context_token_usage INTEGER NOT NULL DEFAULT 0,
    context_window INTEGER NOT NULL DEFAULT 0,
    last_event_seq INTEGER NOT NULL DEFAULT 0,
    deleted_at TEXT,
    pinned_at TEXT,
    last_viewed_at TEXT,
    parent_chat_id TEXT REFERENCES chats(id) ON DELETE SET NULL,
    last_model_id TEXT,
    last_thinking_mode TEXT,
    last_persona_name TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_chats_workspace_deleted ON chats(workspace_id, deleted_at);
CREATE INDEX idx_chats_user_deleted ON chats(user_id, deleted_at);
CREATE INDEX idx_chats_user_updated ON chats(user_id, updated_at);
CREATE INDEX idx_chats_parent ON chats(parent_chat_id);
CREATE INDEX idx_chats_channel ON chats(channel_id);

CREATE TABLE messages (
    id TEXT PRIMARY KEY,
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    content_text TEXT NOT NULL DEFAULT '',
    content_render TEXT NOT NULL DEFAULT '{"events":[]}',
    last_seq INTEGER NOT NULL DEFAULT 0,
    active_stream_id TEXT,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    model_id TEXT,
    session_id TEXT,
    total_cost_usd REAL DEFAULT 0,
    duration_ms INTEGER,
    stream_status TEXT NOT NULL DEFAULT 'in_progress',
    deleted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_messages_chat_created ON messages(chat_id, created_at);
CREATE INDEX idx_messages_chat_role_deleted ON messages(chat_id, role, deleted_at);

CREATE TABLE message_attachments (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    file_url TEXT NOT NULL,
    file_path TEXT NOT NULL,
    file_type TEXT NOT NULL DEFAULT 'image',
    filename TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_message_attachments_message ON message_attachments(message_id);

CREATE TABLE message_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    stream_id TEXT NOT NULL,
    seq INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    render_payload TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_message_events_message_seq ON message_events(message_id, seq);
CREATE INDEX idx_message_events_chat_seq ON message_events(chat_id, seq);

CREATE TABLE chat_checkpoints (
    id TEXT PRIMARY KEY,
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    assistant_message_id TEXT NOT NULL UNIQUE REFERENCES messages(id) ON DELETE CASCADE,
    cwd TEXT,
    base_head TEXT NOT NULL,
    pre_run_diff TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_chat_checkpoints_chat ON chat_checkpoints(chat_id, created_at);

-- Replaces the Redis queue: queued follow-ups survive restarts.
CREATE TABLE queued_messages (
    id TEXT PRIMARY KEY,
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    content TEXT NOT NULL,
    model_id TEXT NOT NULL,
    permission_mode TEXT NOT NULL DEFAULT 'bypassPermissions',
    thinking_mode TEXT,
    worktree INTEGER NOT NULL DEFAULT 0,
    base_branch TEXT,
    selected_persona_name TEXT NOT NULL DEFAULT 'Default',
    attachments TEXT,
    send_now INTEGER NOT NULL DEFAULT 0,
    queued_at TEXT NOT NULL
);
CREATE INDEX idx_queued_messages_chat ON queued_messages(chat_id, position);

CREATE TABLE channel_members (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    chat_id TEXT NOT NULL UNIQUE REFERENCES chats(id) ON DELETE CASCADE,
    permission_mode TEXT NOT NULL,
    model_id TEXT NOT NULL,
    persona TEXT,
    thinking_mode TEXT,
    display_name TEXT NOT NULL,
    introduced INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_members_channel ON channel_members(channel_id);

CREATE TABLE channel_messages (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    member_id TEXT REFERENCES channel_members(id) ON DELETE CASCADE,
    source_message_id TEXT REFERENCES messages(id) ON DELETE SET NULL,
    tool_call_count INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    content TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('streaming', 'completed', 'cancelled', 'deleted')),
    audience TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (channel_id, seq)
);

CREATE TABLE channel_deliveries (
    member_id TEXT NOT NULL REFERENCES channel_members(id) ON DELETE CASCADE,
    message_id TEXT NOT NULL REFERENCES channel_messages(id) ON DELETE CASCADE,
    PRIMARY KEY (member_id, message_id)
);

CREATE TABLE channel_message_attachments (
    id TEXT PRIMARY KEY,
    message_id TEXT NOT NULL REFERENCES channel_messages(id) ON DELETE CASCADE,
    file_url TEXT NOT NULL,
    file_path TEXT NOT NULL,
    file_type TEXT NOT NULL,
    filename TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_message_attachments_message ON channel_message_attachments(message_id);

CREATE TABLE channel_queued_messages (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    content TEXT NOT NULL,
    attachments TEXT,
    queued_at TEXT NOT NULL
);
CREATE INDEX idx_channel_queued_messages_channel ON channel_queued_messages(channel_id, position);

CREATE TABLE automations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    prompt TEXT NOT NULL,
    model_id TEXT NOT NULL,
    permission_mode TEXT NOT NULL DEFAULT 'bypassPermissions',
    thinking_mode TEXT,
    worktree INTEGER NOT NULL DEFAULT 0,
    selected_persona_name TEXT NOT NULL DEFAULT 'Default',
    cron_expression TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC',
    enabled INTEGER NOT NULL DEFAULT 1,
    next_run_at TEXT NOT NULL,
    last_run_at TEXT,
    trigger_type TEXT NOT NULL DEFAULT 'schedule',
    github_repo TEXT,
    github_events TEXT,
    github_cursor TEXT,
    github_webhook_id INTEGER,
    github_webhook_secret TEXT,
    github_webhook_error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_automations_user ON automations(user_id);
CREATE INDEX idx_automations_enabled_next ON automations(enabled, next_run_at);

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chat_id TEXT REFERENCES chats(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    html TEXT NOT NULL,
    is_public INTEGER NOT NULL DEFAULT 0,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_artifacts_user_updated ON artifacts(user_id, updated_at);
