-- domain service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    hostname TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    verification_type TEXT NOT NULL DEFAULT 'cname',
    verification_token TEXT NOT NULL DEFAULT '',
    verified_at TIMESTAMPTZ,
    force_verified BOOLEAN NOT NULL DEFAULT FALSE,
    traefik_file TEXT NOT NULL DEFAULT '',
    certificate_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (hostname)
);
CREATE INDEX IF NOT EXISTS idx_domains_project ON domains(project_id, created_at DESC);
