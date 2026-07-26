package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Build struct {
	ID           string     `json:"id"`
	OrgID        string     `json:"org_id"`
	ProjectID    string     `json:"project_id"`
	DeploymentID *string    `json:"deployment_id,omitempty"`
	Status       string     `json:"status"`
	GitSHA       string     `json:"git_sha"`
	GitBranch    string     `json:"git_branch"`
	CloneURL     string     `json:"clone_url"`
	FullName     string     `json:"full_name"`
	Framework    string     `json:"framework"`
	ImageRef     string     `json:"image_ref"`
	Logs         string     `json:"logs"`
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, b *Build) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	b.CreatedAt = now
	b.UpdatedAt = now
	if b.Status == "" {
		b.Status = "queued"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO builds (id, org_id, project_id, deployment_id, status, git_sha, git_branch, clone_url, full_name, framework, image_ref, logs, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	`, b.ID, b.OrgID, b.ProjectID, b.DeploymentID, b.Status, b.GitSHA, b.GitBranch, b.CloneURL, b.FullName, b.Framework, b.ImageRef, b.Logs, b.Error, b.CreatedAt, b.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Build, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, deployment_id, status, git_sha, git_branch, clone_url, full_name,
		       framework, image_ref, logs, error, created_at, updated_at, started_at, finished_at
		FROM builds WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		var b Build
		if err := rows.Scan(&b.ID, &b.OrgID, &b.ProjectID, &b.DeploymentID, &b.Status, &b.GitSHA, &b.GitBranch, &b.CloneURL, &b.FullName,
			&b.Framework, &b.ImageRef, &b.Logs, &b.Error, &b.CreatedAt, &b.UpdatedAt, &b.StartedAt, &b.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []Build{}
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Build, error) {
	b := &Build{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, deployment_id, status, git_sha, git_branch, clone_url, full_name,
		       framework, image_ref, logs, error, created_at, updated_at, started_at, finished_at
		FROM builds WHERE id=$1
	`, id).Scan(&b.ID, &b.OrgID, &b.ProjectID, &b.DeploymentID, &b.Status, &b.GitSHA, &b.GitBranch, &b.CloneURL, &b.FullName,
		&b.Framework, &b.ImageRef, &b.Logs, &b.Error, &b.CreatedAt, &b.UpdatedAt, &b.StartedAt, &b.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) AppendUpdate(ctx context.Context, id, status, framework, imageRef, logsAppend, errMsg string, started, finished bool) error {
	now := time.Now().UTC()
	q := `
		UPDATE builds SET
			status = COALESCE(NULLIF($2,''), status),
			framework = COALESCE(NULLIF($3,''), framework),
			image_ref = COALESCE(NULLIF($4,''), image_ref),
			logs = CASE WHEN $5 = '' THEN logs ELSE $5 END,
			error = COALESCE(NULLIF($6,''), error),
			updated_at = $7,
			started_at = CASE WHEN $8 THEN COALESCE(started_at, $7) ELSE started_at END,
			finished_at = CASE WHEN $9 THEN $7 ELSE finished_at END
		WHERE id=$1
	`
	_, err := s.pool.Exec(ctx, q, id, status, framework, imageRef, logsAppend, errMsg, now, started, finished)
	return err
}
