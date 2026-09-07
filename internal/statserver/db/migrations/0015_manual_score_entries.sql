CREATE TABLE IF NOT EXISTS manual_score_entries (
  id BIGSERIAL PRIMARY KEY,
  signal_id BIGINT NOT NULL REFERENCES manual_score_signals(id) ON DELETE CASCADE,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  raw_value NUMERIC(20,8) NOT NULL,
  normalized_value NUMERIC(20,8) NOT NULL,
  rationale TEXT NOT NULL DEFAULT '',
  evidence_url TEXT NOT NULL DEFAULT '',
  effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
  superseded_id BIGINT REFERENCES manual_score_entries(id)
);
