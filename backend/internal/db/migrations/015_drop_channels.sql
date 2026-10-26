-- Channels are gone. Their member chats go with them (messages cascade). The
-- channels table stays, empty: chats.channel_id still references it, and
-- SQLite can't drop a foreign-key column without rebuilding chats.
DELETE FROM chats WHERE channel_id IS NOT NULL;
DELETE FROM channels;
DROP TABLE channel_queued_messages;
DROP TABLE channel_message_attachments;
DROP TABLE channel_deliveries;
DROP TABLE channel_messages;
DROP TABLE channel_members;
