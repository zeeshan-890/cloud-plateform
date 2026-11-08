-- explicit preview identity (replaces branch-name heuristics), fork flag, PR number
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS is_preview BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS pr_number INT NOT NULL DEFAULT 0;
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS fork BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE deployments SET is_preview = TRUE
 WHERE NOT is_preview AND (LOWER(source) LIKE '%preview%' OR LOWER(git_branch) LIKE 'preview/%' OR LOWER(message) LIKE '%[preview]%');
-- legacy rows must never carry credential-bearing URLs
UPDATE deployments SET clone_url = '' WHERE clone_url <> '';
-- legacy 'ready' rows that were only built are fine; expired previews were recorded as failed
CREATE INDEX IF NOT EXISTS idx_deployments_preview ON deployments(project_id, is_preview, pr_number, git_branch);
