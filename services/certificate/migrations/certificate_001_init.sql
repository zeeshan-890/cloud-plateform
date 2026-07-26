-- certificate service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    domain_id UUID,
    hostname TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    provider TEXT NOT NULL DEFAULT 'traefik-acme',
    resolver TEXT NOT NULL DEFAULT '',
    issued_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    renewed_at TIMESTAMPTZ,
    renew_before_days INT NOT NULL DEFAULT 30,
    error TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_certs_project ON certificates(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_certs_hostname ON certificates(hostname);
