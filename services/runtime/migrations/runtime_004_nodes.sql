-- multi-server: extra servers (nodes) the runtime can place whole projects on, and where each instance runs
CREATE TABLE IF NOT EXISTS runtime_nodes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    addr TEXT NOT NULL,                 -- node agent host:port on the private network
    private_ip TEXT NOT NULL,           -- address app ports are published on / upstreams point to
    token_sealed TEXT NOT NULL,         -- agent token, sealed (AES-GCM)
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS runtime_project_placement (
    project_id UUID PRIMARY KEY,
    node_id UUID NOT NULL REFERENCES runtime_nodes(id) ON DELETE RESTRICT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runtime_instances ADD COLUMN IF NOT EXISTS host_port INT NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_runtime_instances_node ON runtime_instances(node_id);
