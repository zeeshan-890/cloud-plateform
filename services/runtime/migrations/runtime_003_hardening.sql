-- runtime hardening: truthful state, per-project networks, blue/green, add-ons and one-off job runs
ALTER TABLE runtime_instances ALTER COLUMN mode SET DEFAULT 'docker';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'app';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS service TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS color TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS network TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS health_path TEXT NOT NULL DEFAULT '/';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS replica INT NOT NULL DEFAULT 0;
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS restart_count INT NOT NULL DEFAULT 0;
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS last_restart_at TIMESTAMPTZ;
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS exit_code INT;
CREATE INDEX IF NOT EXISTS idx_runtime_deployment ON runtime_instances(deployment_id);
CREATE INDEX IF NOT EXISTS idx_runtime_container ON runtime_instances(container_id);

CREATE TABLE IF NOT EXISTS runtime_job_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    cron_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    image_ref TEXT NOT NULL DEFAULT '',
    command TEXT[] NOT NULL DEFAULT '{}',
    container_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'running',
    exit_code INT,
    error TEXT NOT NULL DEFAULT '',
    log_tail TEXT NOT NULL DEFAULT '',
    timeout_seconds INT NOT NULL DEFAULT 600,
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_job_runs_project ON runtime_job_runs(project_id, started_at DESC);
