-- billing service schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS billing_usage_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID,
    metric TEXT NOT NULL,
    quantity DOUBLE PRECISION NOT NULL DEFAULT 0,
    unit TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'api',
    meta JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_billing_usage_org ON billing_usage_events(org_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_billing_usage_metric ON billing_usage_events(org_id, metric, created_at DESC);

CREATE TABLE IF NOT EXISTS billing_org_plans (
    org_id UUID PRIMARY KEY,
    plan_id TEXT NOT NULL DEFAULT 'free',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
