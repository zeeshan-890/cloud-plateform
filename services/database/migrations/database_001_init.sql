CREATE TABLE IF NOT EXISTS managed_databases (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    name TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'simulate',
    status TEXT NOT NULL DEFAULT 'ready',
    schema_name TEXT NOT NULL DEFAULT '',
    role_name TEXT NOT NULL DEFAULT '',
    secret_ref TEXT NOT NULL DEFAULT '',
    connection_hint TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id, name)
);

CREATE INDEX IF NOT EXISTS idx_managed_databases_project ON managed_databases (org_id, project_id);
