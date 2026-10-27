-- The model a subagent runs on, for the session panel.
ALTER TABLE chat_subagents ADD COLUMN model TEXT NOT NULL DEFAULT '';
