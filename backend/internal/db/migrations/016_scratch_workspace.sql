-- Threads without a project run in one hidden scratch workspace per user.
CREATE UNIQUE INDEX idx_workspaces_user_scratch ON workspaces(user_id) WHERE source_type = 'scratch' AND deleted_at IS NULL;
