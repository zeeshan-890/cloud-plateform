package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UsageEvent struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	ProjectID *string         `json:"project_id,omitempty"`
	Metric    string          `json:"metric"`
	Quantity  float64         `json:"quantity"`
	Unit      string          `json:"unit"`
	Source    string          `json:"source"`
	Meta      json.RawMessage `json:"meta,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type UsageSummary struct {
	Metric   string  `json:"metric"`
	Quantity float64 `json:"quantity"`
	Unit     string  `json:"unit"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Insert(ctx context.Context, e *UsageEvent) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	e.CreatedAt = now
	if e.Source == "" {
		e.Source = "api"
	}
	var meta any
	if len(e.Meta) > 0 {
		meta = string(e.Meta)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO billing_usage_events (id, org_id, project_id, metric, quantity, unit, source, meta, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::jsonb,$9)
	`, e.ID, e.OrgID, e.ProjectID, e.Metric, e.Quantity, e.Unit, e.Source, meta, e.CreatedAt)
	return err
}

func (s *Store) Summary(ctx context.Context, orgID string, since time.Time) ([]UsageSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT metric, COALESCE(SUM(quantity),0), COALESCE(MAX(unit),'')
		FROM billing_usage_events
		WHERE org_id=$1 AND created_at >= $2
		GROUP BY metric ORDER BY metric
	`, orgID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageSummary
	for rows.Next() {
		var u UsageSummary
		if err := rows.Scan(&u.Metric, &u.Quantity, &u.Unit); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if out == nil {
		out = []UsageSummary{}
	}
	return out, rows.Err()
}

func (s *Store) ListEvents(ctx context.Context, orgID string, limit int) ([]UsageEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, metric, quantity, unit, source, meta, created_at
		FROM billing_usage_events WHERE org_id=$1 ORDER BY created_at DESC LIMIT $2
	`, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageEvent
	for rows.Next() {
		var e UsageEvent
		var meta []byte
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.Metric, &e.Quantity, &e.Unit, &e.Source, &meta, &e.CreatedAt); err != nil {
			return nil, err
		}
		if len(meta) > 0 {
			e.Meta = json.RawMessage(meta)
		}
		out = append(out, e)
	}
	if out == nil {
		out = []UsageEvent{}
	}
	return out, rows.Err()
}

func (s *Store) GetPlan(ctx context.Context, orgID string) (string, error) {
	var plan string
	err := s.pool.QueryRow(ctx, `SELECT plan_id FROM billing_org_plans WHERE org_id=$1`, orgID).Scan(&plan)
	if err != nil {
		return "free", nil
	}
	return plan, nil
}

func (s *Store) SetPlan(ctx context.Context, orgID, planID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO billing_org_plans (org_id, plan_id, updated_at) VALUES ($1,$2,NOW())
		ON CONFLICT (org_id) DO UPDATE SET plan_id=EXCLUDED.plan_id, updated_at=NOW()
	`, orgID, planID)
	return err
}
