ALTER TABLE routing_group_stages ADD COLUMN try_retries INTEGER;
ALTER TABLE routing_group_sources ADD COLUMN provider_name TEXT;
ALTER TABLE routing_group_sources ADD COLUMN retries INTEGER;
ALTER TABLE routing_group_sources ADD COLUMN maximum_official_price_percent INTEGER;
