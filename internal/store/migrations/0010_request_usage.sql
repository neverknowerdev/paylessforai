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
