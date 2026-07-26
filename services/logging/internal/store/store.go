package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Entry struct {
	ID           string         `json:"id"`
	OrgID        string         `json:"org_id"`
	ProjectID    string         `json:"project_id"`
	Source       string         `json:"source"`
	Level        string         `json:"level"`
	Message      string         `json:"message"`
	BuildID      *string        `json:"build_id,omitempty"`
	InstanceID   string         `json:"instance_id,omitempty"`
	DeploymentID *string        `json:"deployment_id,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
	Attrs        map[string]any `json:"attrs,omitempty"`
	LoggedAt     time.Time      `json:"logged_at"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Query struct {
	Source  string
	Level   string
	BuildID string
	Q       string
	Since   *time.Time
	Limit   int
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ingest(ctx context.Context, e *Entry) error {
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if e.LoggedAt.IsZero() {
		e.LoggedAt = now
	}
	e.CreatedAt = now
	if e.Source == "" {
		e.Source = "runtime"
	}
	if e.Level == "" {
		e.Level = "info"
	}
	if e.Attrs == nil {
		e.Attrs = map[string]any{}
	}
	attrs, _ := json.Marshal(e.Attrs)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO log_entries (
			id, org_id, project_id, source, level, message, build_id, instance_id,
			deployment_id, request_id, attrs, logged_at, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, e.ID, e.OrgID, e.ProjectID, e.Source, e.Level, e.Message, e.BuildID, e.InstanceID,
		e.DeploymentID, e.RequestID, attrs, e.LoggedAt, e.CreatedAt)
	return err
}

func (s *Store) Query(ctx context.Context, orgID, projectID string, q Query) ([]Entry, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, source, level, message, build_id, instance_id,
		       deployment_id, request_id, attrs, logged_at, created_at
		FROM log_entries
		WHERE org_id=$1 AND project_id=$2
		  AND ($3 = '' OR source = $3)
		  AND ($4 = '' OR level = $4)
		  AND ($5 = '' OR build_id::text = $5)
		  AND ($6 = '' OR message ILIKE '%' || $6 || '%')
		  AND ($7::timestamptz IS NULL OR logged_at >= $7)
		ORDER BY logged_at DESC
		LIMIT $8
	`, orgID, projectID, q.Source, q.Level, q.BuildID, q.Q, q.Since, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var attrs []byte
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.Source, &e.Level, &e.Message,
			&e.BuildID, &e.InstanceID, &e.DeploymentID, &e.RequestID, &attrs, &e.LoggedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(attrs, &e.Attrs)
		out = append(out, e)
	}
	if out == nil {
		out = []Entry{}
	}
	return out, rows.Err()
}
