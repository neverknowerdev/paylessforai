ALTER TABLE proxy_requests ADD COLUMN selected_provider TEXT;
ALTER TABLE proxy_requests ADD COLUMN selected_upstream_model TEXT;
ALTER TABLE proxy_requests ADD COLUMN attempt_count INTEGER NOT NULL DEFAULT 0;
