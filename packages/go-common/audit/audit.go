package audit

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Event is a platform audit log entry.
type Event struct {
	ActorID    string
	Action     string
	Resource   string
	ResourceID string
	OrgID      string
	Metadata   map[string]any
	IP         string
	UserAgent  string
}

// Writer persists audit events.
type Writer struct {
	pool *pgxpool.Pool
}

func NewWriter(pool *pgxpool.Pool) *Writer {
	return &Writer{pool: pool}
}

func (w *Writer) Write(ctx context.Context, e Event) error {
	meta, err := json.Marshal(e.Metadata)
	if err != nil {
		meta = []byte("{}")
	}
	_, err = w.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, action, resource, resource_id, org_id, metadata, ip, user_agent, created_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)
	`, nullStr(e.ActorID), e.Action, e.Resource, nullStr(e.ResourceID), e.OrgID, meta, nullStr(e.IP), nullStr(e.UserAgent), time.Now().UTC())
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// EnsureSchema creates the audit_logs table if missing (for services that embed audit).
const SchemaSQL = `
CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID,
    action TEXT NOT NULL,
    resource TEXT NOT NULL,
    resource_id TEXT,
    org_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}',
    ip TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_actor ON audit_logs(actor_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created ON audit_logs(created_at DESC);
`
