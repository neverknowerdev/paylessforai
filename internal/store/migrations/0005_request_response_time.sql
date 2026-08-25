ALTER TABLE proxy_requests ADD COLUMN duration_ms INTEGER;

UPDATE proxy_requests
SET duration_ms = CAST((julianday(completed_at) - julianday(received_at)) * 86400000 AS INTEGER)
WHERE duration_ms IS NULL AND completed_at IS NOT NULL AND received_at IS NOT NULL;
