CREATE TABLE IF NOT EXISTS benchmark_results (
  id BIGSERIAL PRIMARY KEY,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  benchmark_name TEXT NOT NULL,
  normalized_name TEXT NOT NULL,
  version TEXT NOT NULL DEFAULT '',
  metric TEXT NOT NULL DEFAULT 'score',
  value NUMERIC(20,8) NOT NULL,
  unit TEXT NOT NULL DEFAULT 'fraction',
  verified BOOLEAN NOT NULL DEFAULT false,
  source_key TEXT NOT NULL,
  observed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX IF NOT EXISTS benchmark_model_idx ON benchmark_results(model_id, normalized_name);
