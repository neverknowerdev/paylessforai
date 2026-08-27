CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE IF NOT EXISTS sources (
  id BIGSERIAL PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  base_url TEXT NOT NULL,
  last_attempt_at TIMESTAMPTZ,
  last_success_at TIMESTAMPTZ,
  last_error TEXT,
  record_count INTEGER NOT NULL DEFAULT 0,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS source_snapshots (
  id BIGSERIAL PRIMARY KEY,
  source_id BIGINT NOT NULL REFERENCES sources(id),
  fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  content_hash TEXT NOT NULL,
  payload JSONB NOT NULL
);

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
CREATE INDEX IF NOT EXISTS models_slug_idx ON models(canonical_slug);
ALTER TABLE models DROP CONSTRAINT IF EXISTS models_canonical_slug_key;
CREATE INDEX IF NOT EXISTS models_name_trgm_idx ON models USING gin (normalized_name gin_trgm_ops);

CREATE TABLE IF NOT EXISTS model_aliases (
  id BIGSERIAL PRIMARY KEY,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  alias TEXT NOT NULL,
  normalized_alias TEXT NOT NULL,
  source_key TEXT NOT NULL,
  valid_from TIMESTAMPTZ NOT NULL DEFAULT now(),
  valid_until TIMESTAMPTZ,
  evidence JSONB NOT NULL DEFAULT '{}'::jsonb,
  UNIQUE(model_id, normalized_alias, source_key, valid_from)
);
CREATE INDEX IF NOT EXISTS model_aliases_norm_idx ON model_aliases(normalized_alias);

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
  metadata JSONB NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS benchmark_model_idx ON benchmark_results(model_id, normalized_name);

CREATE TABLE IF NOT EXISTS telemetry_installations (
  id BIGSERIAL PRIMARY KEY,
  installation_key_hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ
);
CREATE TABLE IF NOT EXISTS telemetry_batches (
  id BIGSERIAL PRIMARY KEY,
  installation_id BIGINT NOT NULL REFERENCES telemetry_installations(id),
  batch_id TEXT NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  event_count INTEGER NOT NULL,
  UNIQUE(installation_id, batch_id)
);
CREATE TABLE IF NOT EXISTS request_observations (
  id BIGSERIAL PRIMARY KEY,
  installation_id BIGINT NOT NULL REFERENCES telemetry_installations(id),
  event_id TEXT NOT NULL,
  model_id BIGINT REFERENCES models(id),
  model_name TEXT NOT NULL DEFAULT '',
  provider TEXT NOT NULL DEFAULT '',
  occurred_at TIMESTAMPTZ NOT NULL,
  total_ms INTEGER,
  ttft_ms INTEGER,
  generation_ms INTEGER,
  input_tokens INTEGER,
  output_tokens INTEGER,
  cached_read_tokens INTEGER,
  cache_write_tokens INTEGER,
  cache_status TEXT NOT NULL DEFAULT 'unknown',
  cache_ttl_seconds INTEGER,
  observed_reuse_age_seconds INTEGER,
  success BOOLEAN NOT NULL DEFAULT true,
  retry_count INTEGER NOT NULL DEFAULT 0,
  cost_usd NUMERIC(20,8),
  metadata JSONB NOT NULL DEFAULT '{}',
  UNIQUE(installation_id, event_id)
);
CREATE INDEX IF NOT EXISTS request_obs_model_time_idx ON request_observations(model_id, occurred_at);

CREATE TABLE IF NOT EXISTS capability_profiles (
  id BIGSERIAL PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  public BOOLEAN NOT NULL DEFAULT true,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
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
CREATE TABLE IF NOT EXISTS capability_profile_components (
  id BIGSERIAL PRIMARY KEY,
  profile_version_id BIGINT NOT NULL REFERENCES capability_profile_versions(id) ON DELETE CASCADE,
  signal_type TEXT NOT NULL,
  benchmark_selector TEXT,
  manual_signal_id BIGINT,
  weight INTEGER NOT NULL CHECK(weight > 0),
  required BOOLEAN NOT NULL DEFAULT false,
  min_value NUMERIC(20,8) NOT NULL DEFAULT 0,
  max_value NUMERIC(20,8) NOT NULL DEFAULT 1,
  direction TEXT NOT NULL DEFAULT 'higher',
  display_order INTEGER NOT NULL DEFAULT 0,
  rationale TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS manual_score_signals (
  id BIGSERIAL PRIMARY KEY,
  key TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  min_value NUMERIC(20,8) NOT NULL DEFAULT 0,
  max_value NUMERIC(20,8) NOT NULL DEFAULT 100,
  active BOOLEAN NOT NULL DEFAULT true
);
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
CREATE TABLE IF NOT EXISTS capability_scores (
  id BIGSERIAL PRIMARY KEY,
  profile_version_id BIGINT NOT NULL REFERENCES capability_profile_versions(id) ON DELETE CASCADE,
  model_id BIGINT NOT NULL REFERENCES models(id) ON DELETE CASCADE,
  score NUMERIC(20,8),
  base_score NUMERIC(20,8),
  coverage NUMERIC(20,8),
  explanation JSONB NOT NULL DEFAULT '{}',
  calculated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE(profile_version_id, model_id)
);

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  email TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  is_admin BOOLEAN NOT NULL DEFAULT false,
  disabled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS admin_sessions (
  id BIGSERIAL PRIMARY KEY,
  token_hash TEXT NOT NULL UNIQUE,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS audit_log (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT REFERENCES users(id),
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail JSONB NOT NULL DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO capability_profiles(key, display_name, description)
VALUES ('programming','Programming','Coding, repository repair, and tool-use capability'),
       ('qa','QA','Factuality, structured output, and defect detection capability'),
       ('cto','CTO','Architecture, planning, coding, and review capability')
ON CONFLICT (key) DO NOTHING;

INSERT INTO capability_profile_versions(profile_id, version, state, minimum_coverage, missing_data_policy, change_note)
SELECT id, 1, 'published', 0, 'linear_penalty', 'Initial profile'
FROM capability_profiles p
WHERE NOT EXISTS (SELECT 1 FROM capability_profile_versions v WHERE v.profile_id=p.id);
