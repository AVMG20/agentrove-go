-- One row per login. The session JWT carries its id (sid); deleting the row
-- signs that login out. Replaces rotating refresh tokens: everyone signs in
-- again once.
CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    user_agent TEXT,
    ip_address TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

DROP TABLE refresh_tokens;
