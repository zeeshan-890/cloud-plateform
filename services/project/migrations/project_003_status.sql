-- project migration 003: lifecycle status (active | deleting) for orchestrated cascade deletes.
ALTER TABLE projects ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS delete_error TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN IF NOT EXISTS deleting_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_projects_status ON projects(status) WHERE status <> 'active';
