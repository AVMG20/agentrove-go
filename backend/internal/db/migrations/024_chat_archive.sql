-- When the chat was archived: hidden from the thread lists, restorable, and
-- deleted once archived for chatArchiveTTL. NULL is not archived.
ALTER TABLE chats ADD COLUMN archived_at TEXT;
CREATE INDEX IF NOT EXISTS idx_chats_archived_at ON chats(archived_at) WHERE archived_at IS NOT NULL;
