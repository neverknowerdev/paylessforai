CREATE TABLE IF NOT EXISTS capability_scores (
  id BIGSERIAL PRIMARY KEY,
  profile_version_id BIGINT NOT NULL REFERENCES capability_profile_versions(id) ON DELETE CASCADE,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  score NUMERIC(20,8),
  base_score NUMERIC(20,8),
  coverage NUMERIC(20,8),
  explanation JSONB NOT NULL DEFAULT '{}'::jsonb,
  calculated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(profile_version_id, model_id)
);
