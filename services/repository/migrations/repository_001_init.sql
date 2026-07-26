-- repository service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS github_installations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    installation_id TEXT NOT NULL,
    account_login TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, installation_id)
);
CREATE INDEX IF NOT EXISTS idx_gh_install_org ON github_installations(org_id);

CREATE TABLE IF NOT EXISTS connected_repos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    installation_id TEXT,
    provider TEXT NOT NULL DEFAULT 'github',
    full_name TEXT NOT NULL,
    clone_url TEXT NOT NULL DEFAULT '',
    default_branch TEXT NOT NULL DEFAULT 'main',
    webhook_secret TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id, full_name)
);
CREATE INDEX IF NOT EXISTS idx_repos_project ON connected_repos(project_id);
CREATE INDEX IF NOT EXISTS idx_repos_full_name ON connected_repos(full_name);
