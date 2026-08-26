CREATE TABLE IF NOT EXISTS client_api_keys (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);
