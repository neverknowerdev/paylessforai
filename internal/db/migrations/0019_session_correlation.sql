CREATE TABLE IF NOT EXISTS sessions (
    client_key_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (client_key_id, session_id)
);

CREATE TABLE IF NOT EXISTS session_keys (
    client_key_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    key_hash TEXT NOT NULL,
    is_anchor INTEGER NOT NULL DEFAULT 0 CHECK (is_anchor IN (0, 1)),
    last_seen_at TEXT NOT NULL,
    PRIMARY KEY (client_key_id, session_id, key_hash),
    FOREIGN KEY (client_key_id, session_id)
        REFERENCES sessions(client_key_id, session_id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS session_keys_lookup
    ON session_keys(client_key_id, key_hash);

CREATE INDEX IF NOT EXISTS session_keys_recent
    ON session_keys(client_key_id, session_id, last_seen_at);

CREATE INDEX IF NOT EXISTS sessions_last_seen
    ON sessions(last_seen_at);

ALTER TABLE proxy_requests ADD COLUMN session_id TEXT;

CREATE INDEX IF NOT EXISTS proxy_requests_session
    ON proxy_requests(client_key_id, session_id);
