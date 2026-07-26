-- metrics service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS metric_samples (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    name TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL DEFAULT 0,
    labels JSONB NOT NULL DEFAULT '{}',
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_metric_samples_project ON metric_samples(project_id, name, recorded_at DESC);

CREATE TABLE IF NOT EXISTS scrape_targets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    job TEXT NOT NULL DEFAULT 'jp-app',
    path TEXT NOT NULL DEFAULT '/metrics',
    port INT NOT NULL DEFAULT 9100,
    instance_id TEXT NOT NULL DEFAULT '',
    annotations JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_scrape_targets_project ON scrape_targets(project_id);
