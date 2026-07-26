-- Phase 7: deploy strategies + applied jp config snapshot
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS strategy TEXT NOT NULL DEFAULT 'rolling';
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS jp_config JSONB;
CREATE INDEX IF NOT EXISTS idx_deployments_strategy ON deployments(project_id, strategy);
