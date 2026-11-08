-- continuous deployment settings of a connection (one project <-> one repository)
-- Defaults reproduce the behaviour from before these settings existed: every push deploys,
-- the default branch is production, other branches and pull requests become previews.
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS root_dir TEXT NOT NULL DEFAULT '';
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS auto_deploy BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS deploy_branch TEXT NOT NULL DEFAULT '';
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS branch_previews BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS pr_previews BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS watch_paths TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS ignore_paths TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE connected_repos ADD COLUMN IF NOT EXISTS require_ci BOOLEAN NOT NULL DEFAULT FALSE;
