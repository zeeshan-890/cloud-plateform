-- Phase 7: last-applied jp.yaml desired state
ALTER TABLE projects ADD COLUMN IF NOT EXISTS jp_config JSONB;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS jp_config_raw TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS jp_config_applied_at TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN IF NOT EXISTS jp_config_hash TEXT NOT NULL DEFAULT '';
