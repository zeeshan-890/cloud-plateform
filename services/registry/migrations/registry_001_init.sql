-- registry metadata schema
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    image_ref TEXT NOT NULL,
    framework TEXT NOT NULL DEFAULT '',
    git_sha TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_images_project ON images(project_id, created_at DESC);
