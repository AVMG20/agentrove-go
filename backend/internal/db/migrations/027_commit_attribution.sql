-- Whether Claude adds its Co-Authored-By trailer to commits and its footer to PRs.
ALTER TABLE user_settings ADD COLUMN commit_attribution INTEGER NOT NULL DEFAULT 1;
