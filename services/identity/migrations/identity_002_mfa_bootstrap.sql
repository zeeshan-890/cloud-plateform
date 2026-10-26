-- identity service migration 002: MFA/TOTP, rate-limiting, and bootstrap controls
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_backup_codes JSONB NOT NULL DEFAULT '[]';

CREATE TABLE IF NOT EXISTS platform_bootstrap (
    id TEXT PRIMARY KEY DEFAULT 'global',
    initialized BOOLEAN NOT NULL DEFAULT FALSE,
    initialized_at TIMESTAMPTZ,
    initialized_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed single row if missing
INSERT INTO platform_bootstrap (id, initialized)
VALUES ('global', FALSE)
ON CONFLICT (id) DO NOTHING;
