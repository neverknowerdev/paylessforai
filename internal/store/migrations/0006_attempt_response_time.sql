ALTER TABLE proxy_attempts ADD COLUMN duration_ms INTEGER;

UPDATE proxy_attempts
SET duration_ms = CAST((julianday(completed_at) - julianday(started_at)) * 86400000 AS INTEGER)
WHERE duration_ms IS NULL AND completed_at IS NOT NULL AND started_at IS NOT NULL;
