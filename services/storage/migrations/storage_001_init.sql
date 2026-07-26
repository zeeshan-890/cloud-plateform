CREATE TABLE IF NOT EXISTS storage_buckets (
    id UUID PRIMARY KEY,
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    name TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'simulate',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id)
);

CREATE TABLE IF NOT EXISTS storage_objects (
    id UUID PRIMARY KEY,
    bucket_id UUID NOT NULL REFERENCES storage_buckets(id) ON DELETE CASCADE,
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    object_key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    etag TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (bucket_id, object_key)
);

CREATE INDEX IF NOT EXISTS idx_storage_objects_project ON storage_objects (org_id, project_id);
