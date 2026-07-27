-- mark preview runtime instances so rolling prod deploys do not stop them
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS is_preview BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX IF NOT EXISTS idx_runtime_preview ON runtime_instances(project_id, is_preview, desired_state);
