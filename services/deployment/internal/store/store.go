package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Deployment struct {
	ID           string          `json:"id"`
	OrgID        string          `json:"org_id"`
	ProjectID    string          `json:"project_id"`
	RepoID       *string         `json:"repo_id,omitempty"`
	BuildID      *string         `json:"build_id,omitempty"`
	RollbackOf   *string         `json:"rollback_of,omitempty"`
	Status       string          `json:"status"`
	Source       string          `json:"source"`
	Strategy     string          `json:"strategy"`
	JPConfig     json.RawMessage `json:"jp_config,omitempty"`
	GitSHA       string          `json:"git_sha"`
	GitBranch    string          `json:"git_branch"`
	CloneURL     string          `json:"clone_url"`
	FullName     string          `json:"full_name"`
	Message      string          `json:"message"`
	ImageRef     string          `json:"image_ref"`
	CommitStatus string          `json:"commit_status"`
	Error        string          `json:"error,omitempty"`
	CreatedBy    *string         `json:"created_by,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const depCols = `id, org_id, project_id, repo_id, build_id, rollback_of, status, source, strategy, jp_config,
	git_sha, git_branch, clone_url, full_name, message, image_ref, commit_status, error, created_by, created_at, updated_at`

func scanDep(row pgx.Row) (*Deployment, error) {
	d := &Deployment{}
	var cfg []byte
	err := row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.RepoID, &d.BuildID, &d.RollbackOf, &d.Status, &d.Source, &d.Strategy, &cfg,
		&d.GitSHA, &d.GitBranch, &d.CloneURL, &d.FullName, &d.Message, &d.ImageRef, &d.CommitStatus, &d.Error, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(cfg) > 0 {
		d.JPConfig = json.RawMessage(cfg)
	}
	if d.Strategy == "" {
		d.Strategy = "rolling"
	}
	return d, nil
}

func scanDepRows(rows pgx.Rows) ([]Deployment, error) {
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		d := Deployment{}
		var cfg []byte
		if err := rows.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.RepoID, &d.BuildID, &d.RollbackOf, &d.Status, &d.Source, &d.Strategy, &cfg,
			&d.GitSHA, &d.GitBranch, &d.CloneURL, &d.FullName, &d.Message, &d.ImageRef, &d.CommitStatus, &d.Error, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if len(cfg) > 0 {
			d.JPConfig = json.RawMessage(cfg)
		}
		if d.Strategy == "" {
			d.Strategy = "rolling"
		}
		out = append(out, d)
	}
	if out == nil {
		out = []Deployment{}
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, d *Deployment) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.Status == "" {
		d.Status = "queued"
	}
	if d.CommitStatus == "" {
		d.CommitStatus = "pending"
	}
	if d.Strategy == "" {
		d.Strategy = "rolling"
	}
	var cfg any
	if len(d.JPConfig) > 0 {
		cfg = string(d.JPConfig)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deployments (
			id, org_id, project_id, repo_id, build_id, rollback_of, status, source, strategy, jp_config,
			git_sha, git_branch, clone_url, full_name, message, image_ref, commit_status, error, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
	`, d.ID, d.OrgID, d.ProjectID, d.RepoID, d.BuildID, d.RollbackOf, d.Status, d.Source, d.Strategy, cfg,
		d.GitSHA, d.GitBranch, d.CloneURL, d.FullName, d.Message, d.ImageRef, d.CommitStatus, d.Error, d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+depCols+` FROM deployments WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return scanDepRows(rows)
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Deployment, error) {
	return scanDep(s.pool.QueryRow(ctx, `
		SELECT `+depCols+` FROM deployments WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID))
}

func (s *Store) GetByID(ctx context.Context, id string) (*Deployment, error) {
	return scanDep(s.pool.QueryRow(ctx, `
		SELECT `+depCols+` FROM deployments WHERE id=$1
	`, id))
}

func (s *Store) LatestSuccessful(ctx context.Context, orgID, projectID string) (*Deployment, error) {
	return scanDep(s.pool.QueryRow(ctx, `
		SELECT `+depCols+` FROM deployments
		WHERE org_id=$1 AND project_id=$2 AND status='ready' AND image_ref <> ''
		ORDER BY created_at DESC LIMIT 1
	`, orgID, projectID))
}

func (s *Store) UpdateStatus(ctx context.Context, id, status, commitStatus, imageRef, errMsg string, buildID *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deployments SET status=$2, commit_status=$3, image_ref=COALESCE(NULLIF($4,''), image_ref),
		error=$5, build_id=COALESCE($6, build_id), updated_at=NOW()
		WHERE id=$1
	`, id, status, commitStatus, imageRef, errMsg, buildID)
	return err
}

// ExpirePreviewDeploys marks old preview-like deployments as expired (failed).
func (s *Store) ExpirePreviewDeploys(ctx context.Context, olderThan time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments SET status='failed', commit_status='failure',
			error=COALESCE(NULLIF(error,''), 'expired preview deploy'), updated_at=NOW()
		WHERE created_at < $1
		  AND status IN ('ready','queued','building')
		  AND (
			LOWER(git_branch) LIKE 'preview/%'
			OR LOWER(git_branch) LIKE '%preview%'
			OR LOWER(message) LIKE '%[preview]%'
		  )
	`, olderThan)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
