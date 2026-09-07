CREATE TABLE IF NOT EXISTS telemetry_installations (
  id BIGSERIAL PRIMARY KEY,
  installation_key_hash TEXT NOT NULL UNIQUE,
  label TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at TIMESTAMPTZ
);
