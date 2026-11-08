package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Sample struct {
	ID         string            `json:"id"`
	OrgID      string            `json:"org_id"`
	ProjectID  string            `json:"project_id"`
	Name       string            `json:"name"`
	Value      float64           `json:"value"`
	Labels     map[string]string `json:"labels,omitempty"`
	RecordedAt time.Time         `json:"recorded_at"`
}

type Summary struct {
	Name       string    `json:"name"`
	Latest     float64   `json:"latest"`
	Count      int       `json:"count"`
	RecordedAt time.Time `json:"recorded_at"`
}

// Point is one value of a series.
type Point struct {
	Value      float64   `json:"value"`
	RecordedAt time.Time `json:"recorded_at"`
}

const MaxSeriesPoints = 1000

// Repo is the persistence surface (faked in tests).
type Repo interface {
	IngestBatch(ctx context.Context, samples []*Sample) error
	Summary(ctx context.Context, orgID, projectID string) ([]Summary, error)
	Series(ctx context.Context, orgID, projectID, name string, since time.Time, limit int) ([]Point, error)
	DeleteProject(ctx context.Context, projectID string) error
	DeleteOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

var _ Repo = (*PG)(nil)

func (s *PG) IngestBatch(ctx context.Context, samples []*Sample) error {
	b := &pgx.Batch{}
	for _, sm := range samples {
		if sm.ID == "" {
			sm.ID = uuid.NewString()
		}
		if sm.RecordedAt.IsZero() {
			sm.RecordedAt = time.Now().UTC()
		}
		if sm.Labels == nil {
			sm.Labels = map[string]string{}
		}
		labels, _ := json.Marshal(sm.Labels)
		b.Queue(`INSERT INTO metric_samples (id, org_id, project_id, name, value, labels, recorded_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			sm.ID, sm.OrgID, sm.ProjectID, sm.Name, sm.Value, labels, sm.RecordedAt)
	}
	res := s.pool.SendBatch(ctx, b)
	defer res.Close()
	for range samples {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// Summary returns the latest value and sample count per metric in one grouped query.
func (s *PG) Summary(ctx context.Context, orgID, projectID string) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (name) name, value, recorded_at,
		       COUNT(*) OVER (PARTITION BY name)
		FROM metric_samples WHERE org_id=$1 AND project_id=$2
		ORDER BY name, recorded_at DESC LIMIT 500
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.Name, &sm.Latest, &sm.RecordedAt, &sm.Count); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

func (s *PG) Series(ctx context.Context, orgID, projectID, name string, since time.Time, limit int) ([]Point, error) {
	if limit <= 0 || limit > MaxSeriesPoints {
		limit = MaxSeriesPoints
	}
	rows, err := s.pool.Query(ctx, `
		SELECT value, recorded_at FROM metric_samples
		WHERE org_id=$1 AND project_id=$2 AND name=$3 AND recorded_at >= $4
		ORDER BY recorded_at DESC LIMIT $5
	`, orgID, projectID, name, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Value, &p.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PG) DeleteProject(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM metric_samples WHERE project_id=$1`, projectID)
	return err
}

func (s *PG) DeleteOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM metric_samples WHERE id IN (SELECT id FROM metric_samples WHERE recorded_at < $1 LIMIT $2)`, cutoff, limit)
	return tag.RowsAffected(), err
}
