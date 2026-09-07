CREATE TABLE IF NOT EXISTS provider_offerings (
  id BIGSERIAL PRIMARY KEY,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  provider TEXT NOT NULL,
  provider_model_id TEXT NOT NULL,
  variant TEXT NOT NULL DEFAULT '',
  input_usd_per_million NUMERIC(20,8),
  output_usd_per_million NUMERIC(20,8),
  cache_read_usd_per_million NUMERIC(20,8),
  cache_write_usd_per_million NUMERIC(20,8),
  context_length BIGINT,
  status TEXT NOT NULL DEFAULT 'active',
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE(provider, provider_model_id)
);
