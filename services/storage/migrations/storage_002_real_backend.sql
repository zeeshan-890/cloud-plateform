-- Buckets recorded by the old in-memory "simulate" backend never existed in MinIO.
DELETE FROM storage_buckets WHERE mode = 'simulate';
ALTER TABLE storage_buckets ALTER COLUMN mode SET DEFAULT 'minio';

-- one bucket per project (lookups are by project only)
CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_buckets_project ON storage_buckets (project_id);
CREATE INDEX IF NOT EXISTS idx_storage_objects_bucket_key ON storage_objects (bucket_id, object_key);
