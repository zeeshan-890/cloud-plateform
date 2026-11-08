-- Domain hardening: real verification, project routes, kinds, DNS health.
ALTER TABLE domains ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'custom';          -- custom | system | preview
ALTER TABLE domains ADD COLUMN IF NOT EXISTS service TEXT;                                   -- explicit traefik service (previews)
ALTER TABLE domains ADD COLUMN IF NOT EXISTS wildcard BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS dns_last_checked TIMESTAMPTZ;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS dns_error TEXT NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN IF NOT EXISTS dns_fail_count INT NOT NULL DEFAULT 0;
ALTER TABLE domains ADD COLUMN IF NOT EXISTS verified_method TEXT NOT NULL DEFAULT '';

-- Rows created before this migration were "verified" by a stubbed DNS check (force/stub default)
-- or are previews. Previews keep working, everything else must be verified for real.
UPDATE domains SET kind = 'preview' WHERE verification_token = 'jp-preview' AND kind = 'custom';
UPDATE domains
   SET status = 'pending', verified_at = NULL, certificate_id = NULL, traefik_file = '',
       verification_token = encode(gen_random_bytes(16), 'hex'),
       dns_error = 'verification reset: previous verification was not backed by a real DNS check'
 WHERE kind = 'custom';

ALTER TABLE domains DROP COLUMN IF EXISTS force_verified;

-- hostname uniqueness: a hostname may be pending in several projects, but only ONE project can hold
-- it as verified/active (and thus have a router). Released on delete.
ALTER TABLE domains DROP CONSTRAINT IF EXISTS domains_hostname_key;
CREATE UNIQUE INDEX IF NOT EXISTS uq_domains_routed_host ON domains (hostname, wildcard) WHERE status IN ('verified', 'active');
CREATE UNIQUE INDEX IF NOT EXISTS uq_domains_project_host ON domains (project_id, hostname, wildcard);
CREATE INDEX IF NOT EXISTS idx_domains_hostname ON domains (hostname);
CREATE INDEX IF NOT EXISTS idx_domains_deployment ON domains (deployment_id) WHERE deployment_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS project_routes (
    project_id UUID PRIMARY KEY,
    service TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
