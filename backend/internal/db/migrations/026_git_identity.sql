-- The name and email the user's agents commit under (GIT_AUTHOR_*/GIT_COMMITTER_*).
ALTER TABLE user_settings ADD COLUMN git_author_name TEXT;
ALTER TABLE user_settings ADD COLUMN git_author_email TEXT;
