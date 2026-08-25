ALTER TABLE request_usage ADD COLUMN official_cost_pico_usd INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_usage ADD COLUMN discount_pico_usd INTEGER;
ALTER TABLE request_usage ADD COLUMN discount_percent_bps INTEGER;
