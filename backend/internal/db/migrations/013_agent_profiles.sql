-- Provider profiles: named agent accounts (several Claude accounts, Codex,
-- Antigravity), each with its own auth in the user's box. The deployment's
-- env keys and the box's ~/claude login are the built-in Claude profile,
-- which has no row.
CREATE TABLE agent_profiles (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL CHECK (provider IN ('claude', 'codex', 'antigravity')),
    name TEXT NOT NULL,
    -- claude: token | endpoint; codex, antigravity: login | token.
    auth_mode TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    usage_url TEXT NOT NULL DEFAULT '',
    -- Encrypted with the app key (auth.Keys); '' when unset.
    token TEXT NOT NULL DEFAULT '',
    -- The CLI config dir relative to the box home; '' = the profile's own
    -- dir. Profiles migrated from env vars keep using ~/claude so their
    -- chats' sessions still resume.
    config_dir TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_agent_profiles_user ON agent_profiles(user_id, created_at);

-- NULL means the built-in Claude profile.
ALTER TABLE user_settings ADD COLUMN default_profile_id TEXT;
-- The profile a chat runs on; NULL (all chats before this) is the built-in
-- one. The provider is kept too, so a chat whose profile was deleted still
-- shows (and refuses to switch) its provider.
ALTER TABLE chats ADD COLUMN profile_id TEXT;
ALTER TABLE chats ADD COLUMN provider TEXT;
