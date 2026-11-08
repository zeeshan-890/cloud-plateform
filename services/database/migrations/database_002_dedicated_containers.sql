-- Managed databases are dedicated Postgres containers provisioned through the add-on service.
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS addon_id UUID;
ALTER TABLE managed_databases ADD COLUMN IF NOT EXISTS error TEXT NOT NULL DEFAULT '';
ALTER TABLE managed_databases ALTER COLUMN mode SET DEFAULT 'container';

-- Rows created by the old implementation lived as a schema + role on the PLATFORM database (or were
-- simulated). They are marked legacy; the service neutralises their login roles at start-up.
UPDATE managed_databases
   SET status = 'failed', mode = 'legacy',
       error = 'legacy shared-schema database: delete it and create a new dedicated database'
 WHERE addon_id IS NULL AND mode <> 'legacy';

CREATE UNIQUE INDEX IF NOT EXISTS uq_managed_databases_project_name ON managed_databases (project_id, name);
