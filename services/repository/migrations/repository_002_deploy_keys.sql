-- project deploy keys for SSH authentication
CREATE TABLE IF NOT EXISTS project_deploy_keys (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL UNIQUE,
    public_key TEXT NOT NULL,
    private_key TEXT NOT NULL,
    key_type TEXT NOT NULL DEFAULT 'ed25519',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_deploy_keys_project ON project_deploy_keys(project_id);
CREATE INDEX IF NOT EXISTS idx_deploy_keys_org ON project_deploy_keys(org_id);
