CREATE TABLE IF NOT EXISTS models (
  id BIGSERIAL PRIMARY KEY,
  canonical_slug TEXT NOT NULL,
  display_name TEXT NOT NULL,
  normalized_name TEXT NOT NULL,
  creator TEXT NOT NULL DEFAULT '',
  family TEXT NOT NULL DEFAULT '',
  revision TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '',
  context_length BIGINT,
  source_key TEXT NOT NULL,
  source_id TEXT NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(source_key, source_id)
);
ALTER TABLE models DROP CONSTRAINT IF EXISTS models_canonical_slug_key;
CREATE INDEX IF NOT EXISTS models_slug_idx ON models(canonical_slug);
CREATE INDEX IF NOT EXISTS models_name_trgm_idx ON models USING gin (normalized_name gin_trgm_ops);
