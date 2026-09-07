CREATE TABLE IF NOT EXISTS capability_profile_components (
  id BIGSERIAL PRIMARY KEY,
  profile_version_id BIGINT NOT NULL REFERENCES capability_profile_versions(id) ON DELETE CASCADE,
  signal_type TEXT NOT NULL,
  benchmark_selector TEXT,
  manual_signal_id BIGINT REFERENCES manual_score_signals(id),
  weight INTEGER NOT NULL CHECK(weight > 0),
  required BOOLEAN NOT NULL DEFAULT false,
  min_value NUMERIC(20,8) NOT NULL DEFAULT 0,
  max_value NUMERIC(20,8) NOT NULL DEFAULT 1,
  direction TEXT NOT NULL DEFAULT 'higher',
  display_order INTEGER NOT NULL DEFAULT 0,
  rationale TEXT NOT NULL DEFAULT ''
);
