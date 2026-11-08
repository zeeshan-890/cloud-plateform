-- Add-ons are REAL containers now.
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'catalog';  -- catalog | custom | managed_db
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS container_id TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS image TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS volume_name TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS secret_keys JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS envs JSONB NOT NULL DEFAULT '[]'::jsonb;
ALTER TABLE managed_addons ADD COLUMN IF NOT EXISTS error TEXT NOT NULL DEFAULT '';

-- Rows created before this migration were simulated (no container, made-up URLs).
UPDATE managed_addons
   SET status = 'failed', mode = 'legacy',
       error = 'legacy simulated add-on: no container exists. Delete it and create a new one.'
 WHERE container_id = '' AND status IN ('ready', 'provisioning');

ALTER TABLE managed_addons DROP CONSTRAINT IF EXISTS managed_addons_org_id_project_id_engine_name_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_addons_project_name ON managed_addons (project_id, name);

CREATE TABLE IF NOT EXISTS addon_backups (
    id UUID PRIMARY KEY,
    addon_id UUID NOT NULL REFERENCES managed_addons(id) ON DELETE CASCADE,
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    engine TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'running',     -- running | completed | failed
    object_key TEXT NOT NULL DEFAULT '',
    size_bytes BIGINT NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    objects_count BIGINT NOT NULL DEFAULT -1,
    error TEXT NOT NULL DEFAULT '',
    last_restore_at TIMESTAMPTZ,
    last_restore_status TEXT NOT NULL DEFAULT '',
    last_restore_error TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_addon_backups_addon ON addon_backups (addon_id, created_at DESC);
