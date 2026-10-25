-- Pinned artifacts stay at the top of the owner's list.
ALTER TABLE artifacts ADD COLUMN pinned_at TEXT;
