-- Certificate status is derived from real TLS probes of the edge; "issued" is never assumed.
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS issuer TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS serial TEXT NOT NULL DEFAULT '';
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS sans JSONB NOT NULL DEFAULT '[]';
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS last_probed_at TIMESTAMPTZ;
ALTER TABLE certificates ADD COLUMN IF NOT EXISTS reissue_requested_at TIMESTAMPTZ;

-- Rows written by the old simulator claimed "issued" without any certificate: reset them so the
-- next probe decides.
UPDATE certificates SET status = 'pending', issued_at = NULL, expires_at = NULL, error = '',
       metadata = '{}'::jsonb WHERE last_probed_at IS NULL;

DELETE FROM certificates a USING certificates b
 WHERE a.domain_id IS NOT NULL AND a.domain_id = b.domain_id
   AND (a.created_at < b.created_at OR (a.created_at = b.created_at AND a.id < b.id));

CREATE UNIQUE INDEX IF NOT EXISTS uq_certs_domain ON certificates (domain_id) WHERE domain_id IS NOT NULL;
