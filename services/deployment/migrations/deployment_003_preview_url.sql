-- preview URL for PR/branch preview deployments
ALTER TABLE deployments ADD COLUMN IF NOT EXISTS preview_url TEXT NOT NULL DEFAULT '';
