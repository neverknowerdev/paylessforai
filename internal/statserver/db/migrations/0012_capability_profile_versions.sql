CREATE TABLE IF NOT EXISTS capability_profile_versions (
  id BIGSERIAL PRIMARY KEY,
  profile_id BIGINT NOT NULL REFERENCES capability_profiles(id) ON DELETE CASCADE,
  version INTEGER NOT NULL,
  state TEXT NOT NULL DEFAULT 'draft',
  minimum_coverage NUMERIC(10,8) NOT NULL DEFAULT 0.5,
  missing_data_policy TEXT NOT NULL DEFAULT 'linear_penalty',
  change_note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ,
  UNIQUE(profile_id, version)
);
