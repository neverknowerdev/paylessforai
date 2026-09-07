CREATE TABLE IF NOT EXISTS telemetry_batches (
  id BIGSERIAL PRIMARY KEY,
  installation_id BIGINT NOT NULL REFERENCES telemetry_installations(id),
  batch_id TEXT NOT NULL,
  received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  event_count INTEGER NOT NULL,
  UNIQUE(installation_id, batch_id)
);
