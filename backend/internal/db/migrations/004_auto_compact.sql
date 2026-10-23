-- Context size (tokens) at which Claude compacts the conversation.
ALTER TABLE user_settings ADD COLUMN auto_compact_window INTEGER NOT NULL DEFAULT 500000;
