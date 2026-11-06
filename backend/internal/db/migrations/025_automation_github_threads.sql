-- One thread per pull request for a GitHub automation: the run for a new PR
-- and every later push continue the same chat. head_sha is the commit the
-- last run was started for (or the first one seen); chat_id is NULL until a
-- run has started for the PR.
CREATE TABLE automation_github_threads (
    automation_id TEXT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    number INTEGER NOT NULL,
    chat_id TEXT,
    head_sha TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (automation_id, number)
);
