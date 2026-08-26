CREATE TABLE IF NOT EXISTS proxy_attempts (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL REFERENCES proxy_requests(id) ON DELETE CASCADE,
    attempt_number INTEGER NOT NULL,
    route_id TEXT,
    provider TEXT,
    upstream_model TEXT,
    state TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    http_status INTEGER,
    error_class TEXT,
    error_message TEXT,
    error_raw TEXT,
    duration_ms INTEGER,
    delivery_state TEXT NOT NULL DEFAULT 'nothing_sent'
);
