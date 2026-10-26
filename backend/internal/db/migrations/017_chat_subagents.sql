-- Subagents Claude spawned in a chat, for the session panel: the prompt, a
-- capped activity log and the outcome. Not the transcript: resuming a session
-- runs on Claude Code's own session files.
CREATE TABLE chat_subagents (
    id TEXT NOT NULL,
    chat_id TEXT NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
    description TEXT NOT NULL DEFAULT '',
    subagent_type TEXT NOT NULL DEFAULT '',
    prompt TEXT NOT NULL DEFAULT '',
    background INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    result TEXT NOT NULL DEFAULT '',
    tool_count INTEGER NOT NULL DEFAULT 0,
    last_activity TEXT NOT NULL DEFAULT '',
    activity TEXT NOT NULL DEFAULT '[]',
    started_at TEXT NOT NULL,
    finished_at TEXT,
    PRIMARY KEY (chat_id, id)
);
CREATE INDEX idx_chat_subagents_chat_started ON chat_subagents(chat_id, started_at);
