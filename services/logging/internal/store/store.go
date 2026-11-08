package store

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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
	// Before is the pagination cursor: only entries logged strictly before it.
	Before *time.Time
	Limit  int
}

const (
	MaxLimit     = 500
	DefaultLimit = 100
)

// Repo is the persistence surface (faked in tests).
type Repo interface {
	IngestBatch(ctx context.Context, entries []*Entry) error
	Query(ctx context.Context, orgID, projectID string, q Query) ([]Entry, error)
	DeleteProject(ctx context.Context, projectID string) (int64, error)
	// DeleteOlderThan removes up to limit entries logged before cutoff and returns how many.
	DeleteOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

var _ Repo = (*PG)(nil)

func normalize(e *Entry) {
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
}

func (s *PG) IngestBatch(ctx context.Context, entries []*Entry) error {
	b := &pgx.Batch{}
	for _, e := range entries {
		normalize(e)
		attrs, _ := json.Marshal(e.Attrs)
		b.Queue(`
			INSERT INTO log_entries (id, org_id, project_id, source, level, message, build_id, instance_id,
				deployment_id, request_id, attrs, logged_at, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			e.ID, e.OrgID, e.ProjectID, e.Source, e.Level, e.Message, e.BuildID, e.InstanceID,
			e.DeploymentID, e.RequestID, attrs, e.LoggedAt, e.CreatedAt)
	}
	res := s.pool.SendBatch(ctx, b)
	defer res.Close()
	for range entries {
		if _, err := res.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (s *PG) Query(ctx context.Context, orgID, projectID string, q Query) ([]Entry, error) {
	if q.Limit <= 0 || q.Limit > MaxLimit {
		q.Limit = DefaultLimit
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, source, level, message, build_id, instance_id,
		       deployment_id, request_id, attrs, logged_at, created_at
		FROM log_entries
		WHERE org_id=$1 AND project_id=$2
		  AND ($3 = '' OR source = $3)
		  AND ($4 = '' OR level = $4)
		  AND ($5 = '' OR build_id::text = $5)
		  AND ($6 = '' OR message ILIKE '%' || $6 || '%' ESCAPE '\')
		  AND ($7::timestamptz IS NULL OR logged_at >= $7)
		  AND ($8::timestamptz IS NULL OR logged_at < $8)
		ORDER BY logged_at DESC
		LIMIT $9
	`, orgID, projectID, q.Source, q.Level, q.BuildID, escapeLike(q.Q), q.Since, q.Before, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
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
	return out, rows.Err()
}

func (s *PG) DeleteProject(ctx context.Context, projectID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM log_entries WHERE project_id=$1`, projectID)
	return tag.RowsAffected(), err
}

func (s *PG) DeleteOlderThan(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM log_entries WHERE id IN (SELECT id FROM log_entries WHERE logged_at < $1 LIMIT $2)`, cutoff, limit)
	return tag.RowsAffected(), err
}
