-- credentials move into encrypted columns; clone URLs never carry tokens
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS auth_type TEXT NOT NULL DEFAULT 'none';
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS token_enc BYTEA;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS webhook_secret_enc BYTEA;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS allow_forks BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS legacy_scrubbed BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE project_deploy_keys ADD COLUMN IF NOT EXISTS private_key_enc BYTEA;
ALTER TABLE project_deploy_keys ALTER COLUMN private_key SET DEFAULT '';
-- an installation belongs to exactly one organization
CREATE UNIQUE INDEX IF NOT EXISTS uq_gh_installation_global ON github_installations(installation_id);
CREATE INDEX IF NOT EXISTS idx_repos_installation ON connected_repos(installation_id);
