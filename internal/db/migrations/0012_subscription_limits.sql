ALTER TABLE provider_credentials ADD COLUMN access_mode TEXT NOT NULL DEFAULT 'api';
ALTER TABLE provider_credentials ADD COLUMN subscription_fee_pico_usd INTEGER;
ALTER TABLE provider_credentials ADD COLUMN subscription_cycle_start TEXT;
ALTER TABLE provider_credentials ADD COLUMN subscription_cycle_end TEXT;
ALTER TABLE provider_credentials ADD COLUMN subscription_status TEXT NOT NULL DEFAULT 'available';
ALTER TABLE provider_credentials ADD COLUMN next_available_at TEXT;
ALTER TABLE provider_credentials ADD COLUMN status_reason TEXT;
ALTER TABLE proxy_requests ADD COLUMN stats_disposition TEXT NOT NULL DEFAULT 'included';
ALTER TABLE proxy_attempts ADD COLUMN stats_disposition TEXT NOT NULL DEFAULT 'included';
