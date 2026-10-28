-- The token that replaced a rotated one, encrypted, so a replay within the
-- rotation grace (a second tab, or a client that never got the refresh
-- response) gets the same live token back instead of being signed out.
-- Cleared once the grace window has passed.
ALTER TABLE refresh_tokens ADD COLUMN replaced_by TEXT;
