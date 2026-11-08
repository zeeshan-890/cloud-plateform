-- Per-organisation AI configuration. API keys are encrypted at rest (AES-256-GCM) and never returned.
CREATE TABLE IF NOT EXISTS ai_config (
    org_id UUID PRIMARY KEY,
    provider TEXT NOT NULL,                 -- gemini | openai (openai-compatible)
    model TEXT NOT NULL,
    base_url TEXT NOT NULL DEFAULT '',
    encrypted_key BYTEA,
    key_last4 TEXT NOT NULL DEFAULT '',
    updated_by UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
