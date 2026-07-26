-- runtime service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS runtime_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    kind TEXT NOT NULL DEFAULT 'container',
    image_ref TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'desired',
    desired_state TEXT NOT NULL DEFAULT 'running',
    container_id TEXT NOT NULL DEFAULT '',
    container_name TEXT NOT NULL DEFAULT '',
    slot TEXT NOT NULL DEFAULT 'node-1',
    port INT NOT NULL DEFAULT 8080,
    restart_policy TEXT NOT NULL DEFAULT 'on-failure',
    mode TEXT NOT NULL DEFAULT 'simulate',
    error TEXT NOT NULL DEFAULT '',
    health_status TEXT NOT NULL DEFAULT 'unknown',
    last_health_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_runtime_project ON runtime_instances(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_runtime_desired ON runtime_instances(desired_state, status);
