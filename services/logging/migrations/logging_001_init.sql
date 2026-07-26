-- logging service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS log_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    source TEXT NOT NULL DEFAULT 'runtime',
    level TEXT NOT NULL DEFAULT 'info',
    message TEXT NOT NULL,
    build_id UUID,
    instance_id TEXT NOT NULL DEFAULT '',
    deployment_id UUID,
    request_id TEXT NOT NULL DEFAULT '',
    attrs JSONB NOT NULL DEFAULT '{}',
    logged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_log_entries_project ON log_entries(project_id, logged_at DESC);
CREATE INDEX IF NOT EXISTS idx_log_entries_source ON log_entries(project_id, source, logged_at DESC);
CREATE INDEX IF NOT EXISTS idx_log_entries_build ON log_entries(build_id) WHERE build_id IS NOT NULL;
