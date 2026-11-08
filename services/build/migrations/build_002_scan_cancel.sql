ALTER TABLE builds ADD COLUMN IF NOT EXISTS repo_id UUID;
ALTER TABLE builds ADD COLUMN IF NOT EXISTS pr_number INT NOT NULL DEFAULT 0;
ALTER TABLE builds ADD COLUMN IF NOT EXISTS scan_status TEXT NOT NULL DEFAULT '';
ALTER TABLE builds ADD COLUMN IF NOT EXISTS scan JSONB;
ALTER TABLE builds ADD COLUMN IF NOT EXISTS logs_truncated BOOLEAN NOT NULL DEFAULT FALSE;
-- never keep credentials-bearing URLs from the old pipeline
UPDATE builds SET clone_url = '' WHERE clone_url <> '';
CREATE INDEX IF NOT EXISTS idx_builds_deployment ON builds(deployment_id);
