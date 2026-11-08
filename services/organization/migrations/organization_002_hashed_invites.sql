-- organization migration 002: invite tokens are stored hashed (SHA-256 hex) only.
ALTER TABLE org_invites ADD COLUMN IF NOT EXISTS token_hash TEXT;
ALTER TABLE org_invites ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ;

-- Backfill hashes for invites created before this migration, then drop the plaintext.
UPDATE org_invites SET token_hash = encode(digest(token, 'sha256'), 'hex')
 WHERE token_hash IS NULL AND token IS NOT NULL;

ALTER TABLE org_invites ALTER COLUMN token DROP NOT NULL;
UPDATE org_invites SET token = NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_org_invites_token_hash ON org_invites(token_hash);
CREATE INDEX IF NOT EXISTS idx_org_invites_org ON org_invites(org_id) WHERE accepted_at IS NULL AND revoked_at IS NULL;
