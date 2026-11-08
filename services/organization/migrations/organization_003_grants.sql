-- organization migration 003: per-project access grants for restricted users (members / viewers).
ALTER TABLE org_members ADD COLUMN IF NOT EXISTS managed BOOLEAN NOT NULL DEFAULT FALSE;

CREATE TABLE IF NOT EXISTS project_grants (
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    user_id UUID NOT NULL,
    preset TEXT NOT NULL DEFAULT 'custom',
    areas JSONB NOT NULL DEFAULT '{}',
    secrets_reveal BOOLEAN NOT NULL DEFAULT FALSE,
    granted_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_project_grants_user ON project_grants(org_id, user_id);

-- Existing installs: members used to see every project. Keep that working by granting the
-- "developer" preset (viewers: "viewer") on the projects that exist today. New projects are NOT
-- granted automatically - restricted users only see what an admin grants them.
DO $$
BEGIN
  IF to_regclass('public.projects') IS NOT NULL THEN
    INSERT INTO project_grants (org_id, project_id, user_id, preset, areas, secrets_reveal)
    SELECT m.org_id, p.id, m.user_id,
           CASE WHEN m.role = 'viewer' THEN 'viewer' ELSE 'developer' END,
           CASE WHEN m.role = 'viewer'
                THEN '{"overview":"view","deployments":"view","builds":"view","runtime":"view","logs":"view","metrics":"view","domains":"view"}'::jsonb
                ELSE '{"overview":"view","deployments":"manage","builds":"manage","runtime":"manage","storage":"manage","logs":"view","metrics":"view","domains":"view","repository":"view","secrets":"view","addons":"view","databases":"view"}'::jsonb
           END,
           FALSE
    FROM org_members m JOIN projects p ON p.org_id = m.org_id
    WHERE m.role IN ('member', 'viewer')
    ON CONFLICT DO NOTHING;
  END IF;
END $$;
