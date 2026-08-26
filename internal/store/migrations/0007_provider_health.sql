CREATE TABLE IF NOT EXISTS provider_health (
    route_id TEXT PRIMARY KEY,
    failure_count INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'healthy',
    backoff_until TEXT,
    last_error TEXT,
    updated_at TEXT NOT NULL
);
