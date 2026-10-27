-- A login's stable id across refresh rotations, so Settings can list and
-- revoke sessions. Rotation copies session_id and created_at to the new row,
-- so created_at is when the session began and an active row's updated_at is
-- its last refresh.
ALTER TABLE refresh_tokens ADD COLUMN session_id TEXT;
UPDATE refresh_tokens SET session_id = id WHERE session_id IS NULL;
CREATE INDEX idx_refresh_tokens_user_session ON refresh_tokens(user_id, session_id);
