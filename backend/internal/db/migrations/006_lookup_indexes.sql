-- Indexes for foreign-key lookups and ON DELETE cascades that otherwise scan
-- the whole child table (channel_deliveries' primary key leads with member_id).
CREATE INDEX IF NOT EXISTS idx_channel_messages_source ON channel_messages(source_message_id);
CREATE INDEX IF NOT EXISTS idx_channel_messages_member ON channel_messages(member_id);
CREATE INDEX IF NOT EXISTS idx_channel_deliveries_message ON channel_deliveries(message_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_chat ON artifacts(chat_id);
CREATE INDEX IF NOT EXISTS idx_chats_automation ON chats(automation_id);
