-- logging migration 002: index for retention sweeps (LOG_RETENTION_DAYS).
CREATE INDEX IF NOT EXISTS idx_log_entries_logged_at ON log_entries(logged_at);
