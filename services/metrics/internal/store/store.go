package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Sample struct {
	ID         string             `json:"id"`
	OrgID      string             `json:"org_id"`
	ProjectID  string             `json:"project_id"`
	Name       string             `json:"name"`
	Value      float64            `json:"value"`
	Labels     map[string]string  `json:"labels,omitempty"`
	RecordedAt time.Time          `json:"recorded_at"`
}

type Target struct {
	ID          string         `json:"id"`
	OrgID       string         `json:"org_id"`
	ProjectID   string         `json:"project_id"`
	Job         string         `json:"job"`
	Path        string         `json:"path"`
	Port        int            `json:"port"`
	InstanceID  string         `json:"instance_id,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Summary struct {
	Name       string    `json:"name"`
	Latest     float64   `json:"latest"`
	Count      int       `json:"count"`
	RecordedAt time.Time `json:"recorded_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Ingest(ctx context.Context, sample *Sample) error {
	if sample.ID == "" {
		sample.ID = uuid.NewString()
	}
	if sample.RecordedAt.IsZero() {
		sample.RecordedAt = time.Now().UTC()
	}
	if sample.Labels == nil {
		sample.Labels = map[string]string{}
	}
	labels, _ := json.Marshal(sample.Labels)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO metric_samples (id, org_id, project_id, name, value, labels, recorded_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, sample.ID, sample.OrgID, sample.ProjectID, sample.Name, sample.Value, labels, sample.RecordedAt)
	return err
}

func (s *Store) Summary(ctx context.Context, orgID, projectID string) ([]Summary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (name) name, value, recorded_at,
		       (SELECT COUNT(*) FROM metric_samples s2 WHERE s2.project_id=$2 AND s2.name=s1.name) AS cnt
		FROM metric_samples s1
		WHERE org_id=$1 AND project_id=$2
		ORDER BY name, recorded_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var sm Summary
		if err := rows.Scan(&sm.Name, &sm.Latest, &sm.RecordedAt, &sm.Count); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	if out == nil {
		out = []Summary{}
	}
	return out, rows.Err()
}

func (s *Store) ListTargets(ctx context.Context, orgID, projectID string) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, job, path, port, instance_id, annotations, created_at
		FROM scrape_targets WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Target
	for rows.Next() {
		var t Target
		var ann []byte
		if err := rows.Scan(&t.ID, &t.OrgID, &t.ProjectID, &t.Job, &t.Path, &t.Port, &t.InstanceID, &ann, &t.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(ann, &t.Annotations)
		out = append(out, t)
	}
	if out == nil {
		out = []Target{}
	}
	return out, rows.Err()
}

func (s *Store) UpsertTarget(ctx context.Context, t *Target) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	t.CreatedAt = time.Now().UTC()
	if t.Job == "" {
		t.Job = "jp-app"
	}
	if t.Path == "" {
		t.Path = "/metrics"
	}
	if t.Port == 0 {
		t.Port = 9100
	}
	if t.Annotations == nil {
		t.Annotations = map[string]any{}
	}
	ann, _ := json.Marshal(t.Annotations)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO scrape_targets (id, org_id, project_id, job, path, port, instance_id, annotations, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, t.ID, t.OrgID, t.ProjectID, t.Job, t.Path, t.Port, t.InstanceID, ann, t.CreatedAt)
	return err
}

func (s *Store) SeedSimulate(ctx context.Context, orgID, projectID string) error {
	now := time.Now().UTC()
	samples := []struct {
		name  string
		value float64
	}{
		{"http_requests_total", float64(100 + now.Second()*3)},
		{"http_request_duration_ms", 42 + float64(now.Second()%10)},
		{"build_jobs_total", float64(now.Minute())},
		{"runtime_instances_up", 1},
	}
	for _, s0 := range samples {
		_ = s.Ingest(ctx, &Sample{
			OrgID: orgID, ProjectID: projectID, Name: s0.name, Value: s0.value,
			Labels: map[string]string{"mode": "simulate"}, RecordedAt: now,
		})
	}
	return nil
}
