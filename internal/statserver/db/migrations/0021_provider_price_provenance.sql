ALTER TABLE provider_offerings
  ADD COLUMN IF NOT EXISTS official_input_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS official_output_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS official_cache_read_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS official_cache_write_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS official_price_source TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS official_price_source_url TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS official_price_observed_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS override_input_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS override_output_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS override_cache_read_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS override_cache_write_usd_per_million NUMERIC(20,8),
  ADD COLUMN IF NOT EXISTS override_updated_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS override_updated_by BIGINT REFERENCES users(id);

UPDATE provider_offerings
SET official_input_usd_per_million = COALESCE(official_input_usd_per_million, input_usd_per_million),
    official_output_usd_per_million = COALESCE(official_output_usd_per_million, output_usd_per_million),
    official_cache_read_usd_per_million = COALESCE(official_cache_read_usd_per_million, cache_read_usd_per_million),
    official_cache_write_usd_per_million = COALESCE(official_cache_write_usd_per_million, cache_write_usd_per_million),
    official_price_source = CASE WHEN official_price_source='' THEN provider ELSE official_price_source END,
    official_price_observed_at = COALESCE(official_price_observed_at, observed_at)
WHERE official_input_usd_per_million IS NULL
   OR official_output_usd_per_million IS NULL
   OR official_price_source='';
