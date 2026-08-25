CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS provider_credentials (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    label TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    ciphertext BLOB NOT NULL,
    nonce BLOB NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    last_checked_at TEXT,
    last_error TEXT
);

CREATE TABLE IF NOT EXISTS client_api_keys (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at TEXT
);

CREATE TABLE IF NOT EXISTS catalog_refreshes (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    error TEXT
);

CREATE TABLE IF NOT EXISTS models (
    id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    context_length INTEGER NOT NULL DEFAULT 0,
    max_output_tokens INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    observed_at TEXT NOT NULL,
    stale_at TEXT
);

CREATE TABLE IF NOT EXISTS model_routes (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    protocol TEXT NOT NULL,
    price_json TEXT NOT NULL,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    health TEXT NOT NULL DEFAULT 'healthy',
    trusted INTEGER NOT NULL DEFAULT 0,
    observed_at TEXT NOT NULL,
    stale_at TEXT
);

CREATE TABLE IF NOT EXISTS provider_health (
    route_id TEXT PRIMARY KEY,
    failure_count INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL DEFAULT 'healthy',
    backoff_until TEXT,
    last_error TEXT,
    updated_at TEXT NOT NULL
);

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

CREATE TABLE IF NOT EXISTS request_usage (
    request_id TEXT PRIMARY KEY REFERENCES proxy_requests(id) ON DELETE CASCADE,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    cached_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    estimated_cost_pico_usd INTEGER NOT NULL DEFAULT 0,
    official_cost_pico_usd INTEGER NOT NULL DEFAULT 0,
    discount_pico_usd INTEGER,
    discount_percent_bps INTEGER,
    actual_cost_pico_usd INTEGER,
    raw_usage_json TEXT NOT NULL DEFAULT '{}'
);
