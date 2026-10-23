-- Past versions of each artifact, so a bad edit can be rolled back.
CREATE TABLE artifact_versions (
    artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    title TEXT NOT NULL,
    html TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (artifact_id, version)
);
INSERT INTO artifact_versions (artifact_id, version, title, html, created_at)
    SELECT id, version, title, html, updated_at FROM artifacts;

-- Script errors the owner's viewer saw, reported back to the agent.
CREATE TABLE artifact_errors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    version INTEGER NOT NULL,
    message TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (artifact_id, version, message)
);

-- Key/value state the page keeps through Kit.store; values are JSON.
CREATE TABLE artifact_state (
    artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (artifact_id, key)
);
