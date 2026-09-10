-- Route protocol compatibility used to describe the caller, which made it
-- impossible to learn a provider's actual endpoint. Rebuild the small SQLite
-- table so the replacement field is nullable and contains only upstream
-- format evidence. Historical values were caller capabilities, not evidence.
ALTER TABLE model_routes RENAME TO model_routes_legacy_0018;

CREATE TABLE model_routes (
    id TEXT PRIMARY KEY,
    model_id TEXT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    format TEXT NULL,
    price_json TEXT NOT NULL,
    capabilities_json TEXT NOT NULL DEFAULT '{}',
    health TEXT NOT NULL DEFAULT 'healthy',
    trusted INTEGER NOT NULL DEFAULT 0,
    observed_at TEXT NOT NULL,
    stale_at TEXT
);

INSERT INTO model_routes (id, model_id, provider, upstream_model, format, price_json, capabilities_json, health, trusted, observed_at, stale_at)
SELECT id, model_id, provider, upstream_model, NULL, price_json, capabilities_json, health, trusted, observed_at, stale_at
FROM model_routes_legacy_0018;

DROP TABLE model_routes_legacy_0018;

ALTER TABLE proxy_attempts ADD COLUMN client_format TEXT NULL;
ALTER TABLE proxy_attempts ADD COLUMN provider_format TEXT NULL;
