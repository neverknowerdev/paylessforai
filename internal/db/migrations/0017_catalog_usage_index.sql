CREATE INDEX IF NOT EXISTS idx_proxy_requests_catalog_usage
    ON proxy_requests (received_at, selected_provider, selected_upstream_model);
