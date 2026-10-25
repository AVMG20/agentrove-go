-- Sub-threads are gone: promote existing ones to top-level chats (column kept).
UPDATE chats SET parent_chat_id = NULL WHERE parent_chat_id IS NOT NULL;
