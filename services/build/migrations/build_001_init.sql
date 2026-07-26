-- build service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS builds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    status TEXT NOT NULL DEFAULT 'queued',
    git_sha TEXT NOT NULL DEFAULT '',
    git_branch TEXT NOT NULL DEFAULT 'main',
    clone_url TEXT NOT NULL DEFAULT '',
    full_name TEXT NOT NULL DEFAULT '',
    framework TEXT NOT NULL DEFAULT '',
    image_ref TEXT NOT NULL DEFAULT '',
    logs TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_builds_project ON builds(project_id, created_at DESC);
