-- The env var a Claude profile's token is passed in ('' = by auth mode:
-- ANTHROPIC_AUTH_TOKEN for an endpoint, else CLAUDE_CODE_OAUTH_TOKEN for an
-- OAuth token and ANTHROPIC_API_KEY otherwise). A profile migrated from env
-- vars keeps the variable the user had set, e.g. ANTHROPIC_API_KEY for a
-- gateway that takes x-api-key.
ALTER TABLE agent_profiles ADD COLUMN token_env TEXT NOT NULL DEFAULT '';
-- Set once the user's Claude env vars were looked at by the move into
-- profiles: the vars a profile didn't take are kept, so their presence alone
-- can't mark a user as done.
ALTER TABLE user_settings ADD COLUMN profiles_env_migrated INTEGER NOT NULL DEFAULT 0;
