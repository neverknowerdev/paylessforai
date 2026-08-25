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
