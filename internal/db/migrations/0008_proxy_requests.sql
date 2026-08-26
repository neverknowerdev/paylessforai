CREATE TABLE IF NOT EXISTS proxy_requests (
    id TEXT PRIMARY KEY,
    client_key_id TEXT,
    protocol TEXT NOT NULL,
    logical_model TEXT NOT NULL,
    state TEXT NOT NULL,
    received_at TEXT NOT NULL,
    completed_at TEXT,
    selected_provider TEXT,
    selected_upstream_model TEXT,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    error_code TEXT,
    error_message TEXT
);
