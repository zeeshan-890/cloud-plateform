-- identity service migration 003: platform admin, encrypted/hashed MFA material, PAT expiry,
-- password change tracking, platform settings.
-- NOTE: plaintext TOTP seeds / backup codes written by migration 002 are re-encrypted / hashed
-- by the identity service at startup (store.UpgradeLegacyMFA) because that needs the app key.

ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_pending_secret TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_changed_at TIMESTAMPTZ;

ALTER TABLE personal_access_tokens ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

-- Backup codes are stored as keyed hashes (HMAC-SHA256) only, one row per code.
CREATE TABLE IF NOT EXISTS identity_backup_codes (
    id BIGSERIAL PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, code_hash)
);
CREATE INDEX IF NOT EXISTS idx_backup_codes_user ON identity_backup_codes(user_id) WHERE used_at IS NULL;

-- Runtime platform settings (SMTP + AI keys). Secret values are AES-GCM sealed by the service.
CREATE TABLE IF NOT EXISTS platform_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    is_secret BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by UUID
);

-- Sessions: fast lookups by user for revoke-all.
CREATE INDEX IF NOT EXISTS idx_sessions_refresh ON sessions(refresh_jti);

-- Existing installs: the user who bootstrapped the platform (or, failing that, the oldest
-- user) becomes the platform admin so the system pages stay reachable.
UPDATE users SET is_admin = TRUE
 WHERE id = (SELECT initialized_by FROM platform_bootstrap WHERE id = 'global' AND initialized_by IS NOT NULL)
   AND NOT EXISTS (SELECT 1 FROM users WHERE is_admin);

UPDATE users SET is_admin = TRUE
 WHERE id = (SELECT id FROM users ORDER BY created_at ASC LIMIT 1)
   AND NOT EXISTS (SELECT 1 FROM users WHERE is_admin);

-- A platform that already has users is initialised (closes the open bootstrap endpoint).
UPDATE platform_bootstrap SET initialized = TRUE, initialized_at = COALESCE(initialized_at, NOW())
 WHERE id = 'global' AND EXISTS (SELECT 1 FROM users);
