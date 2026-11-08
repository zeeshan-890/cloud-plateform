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

var (
	ErrNotFound = errors.New("not found")
	// ErrBadTransition: the requested status change is not allowed from the current state.
	ErrBadTransition = errors.New("invalid status transition")
)

// Deployment states. queued -> building -> deploying -> ready; any non-terminal state may
// fail or be canceled; ready may be superseded (newer production deploy) or stopped
// (preview torn down).
const (
	Queued     = "queued"
	Building   = "building"
	Deploying  = "deploying"
	Ready      = "ready"
	Failed     = "failed"
	Canceled   = "canceled"
	Superseded = "superseded"
	Stopped    = "stopped"
)

var transitions = map[string][]string{
	Queued:     {Building, Failed, Canceled},
	Building:   {Deploying, Failed, Canceled},
	Deploying:  {Ready, Failed, Canceled},
	Ready:      {Superseded, Stopped, Failed},
	Superseded: {Stopped},
}

// ValidStatus reports whether s is a known state.
func ValidStatus(s string) bool {
	switch s {
	case Queued, Building, Deploying, Ready, Failed, Canceled, Superseded, Stopped:
		return true
	}
	return false
}

// IsTerminal reports states that never change again (except ready/superseded cleanups).
func IsTerminal(s string) bool { return s == Failed || s == Canceled || s == Stopped }

// CanTransition implements the state machine; same-state updates are idempotent no-ops.
func CanTransition(from, to string) bool {
	if from == to {
		return true
	}
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Deployment never carries a clone URL or any credential.
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
	FullName     string          `json:"full_name"`
	Message      string          `json:"message"`
	ImageRef     string          `json:"image_ref"`
	CommitStatus string          `json:"commit_status"`
	PreviewURL   string          `json:"preview_url,omitempty"`
	IsPreview    bool            `json:"is_preview"`
	PRNumber     int             `json:"pr_number,omitempty"`
	Fork         bool            `json:"fork,omitempty"`
	Error        string          `json:"error,omitempty"`
	CreatedBy    *string         `json:"created_by,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Patch is an internal status update; empty fields are left unchanged.
type Patch struct {
	Status     string
	ImageRef   string
	Error      string
	BuildID    string
	GitSHA     string
	JPConfig   json.RawMessage
	PreviewURL *string
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const depCols = `id, org_id, project_id, repo_id, build_id, rollback_of, status, source, strategy, jp_config,
	git_sha, git_branch, full_name, message, image_ref, commit_status, COALESCE(preview_url,''), is_preview, pr_number, fork,
	error, created_by, created_at, updated_at`

func scanDep(row pgx.Row) (*Deployment, error) {
	d := &Deployment{}
	var cfg []byte
	err := row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.RepoID, &d.BuildID, &d.RollbackOf, &d.Status, &d.Source, &d.Strategy, &cfg,
		&d.GitSHA, &d.GitBranch, &d.FullName, &d.Message, &d.ImageRef, &d.CommitStatus, &d.PreviewURL, &d.IsPreview, &d.PRNumber, &d.Fork,
		&d.Error, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
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

func scanRows(rows pgx.Rows) ([]Deployment, error) {
	defer rows.Close()
	out := []Deployment{}
	for rows.Next() {
		d, err := scanDep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func CommitStateFor(status string) string {
	switch status {
	case Ready:
		return "success"
	case Failed, Canceled:
		return "failure"
	}
	return "pending"
}

func (s *Store) Create(ctx context.Context, d *Deployment) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	if d.Status == "" {
		d.Status = Queued
	}
	d.CommitStatus = CommitStateFor(d.Status)
	if d.Strategy == "" {
		d.Strategy = "rolling"
	}
	var cfg any
	if len(d.JPConfig) > 0 {
		cfg = string(d.JPConfig)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO deployments (id, org_id, project_id, repo_id, build_id, rollback_of, status, source, strategy, jp_config,
			git_sha, git_branch, clone_url, full_name, message, image_ref, commit_status, preview_url, is_preview, pr_number, fork,
			error, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::jsonb,$11,$12,'',$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24)
	`, d.ID, d.OrgID, d.ProjectID, d.RepoID, d.BuildID, d.RollbackOf, d.Status, d.Source, d.Strategy, cfg,
		d.GitSHA, d.GitBranch, d.FullName, d.Message, d.ImageRef, d.CommitStatus, d.PreviewURL, d.IsPreview, d.PRNumber, d.Fork,
		d.Error, d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Deployment, error) {
	return scanDep(s.pool.QueryRow(ctx, `SELECT `+depCols+` FROM deployments WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID))
}

func (s *Store) GetByID(ctx context.Context, id string) (*Deployment, error) {
	return scanDep(s.pool.QueryRow(ctx, `SELECT `+depCols+` FROM deployments WHERE id=$1`, id))
}

// RollbackCandidates returns production deployments that went live (newest first).
func (s *Store) RollbackCandidates(ctx context.Context, orgID, projectID string, limit int) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments
		WHERE org_id=$1 AND project_id=$2 AND NOT is_preview AND status IN ('ready','superseded') AND image_ref <> ''
		ORDER BY created_at DESC LIMIT $3`, orgID, projectID, limit)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// Apply performs a validated, idempotent status update. changed is false when the
// deployment already was in the requested status.
func (s *Store) Apply(ctx context.Context, id string, p Patch) (d *Deployment, changed bool, err error) {
	cur, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if p.Status != "" && !CanTransition(cur.Status, p.Status) {
		return cur, false, ErrBadTransition
	}
	changed = p.Status != "" && p.Status != cur.Status
	status := cur.Status
	if p.Status != "" {
		status = p.Status
	}
	var cfg any
	if len(p.JPConfig) > 0 {
		cfg = string(p.JPConfig)
	}
	var build any
	if p.BuildID != "" {
		build = p.BuildID
	}
	var preview any
	if p.PreviewURL != nil {
		preview = *p.PreviewURL
	}
	// the WHERE clause re-checks the status we validated against (compare-and-set)
	tag, err := s.pool.Exec(ctx, `
		UPDATE deployments SET status=$3, commit_status=$4,
			image_ref=COALESCE(NULLIF($5,''), image_ref),
			error=CASE WHEN $3 IN ('failed','canceled') THEN $6 WHEN $6 <> '' THEN $6 ELSE CASE WHEN $3 = 'ready' THEN '' ELSE error END END,
			build_id=COALESCE($7::uuid, build_id),
			git_sha=COALESCE(NULLIF($8,''), git_sha),
			jp_config=COALESCE($9::jsonb, jp_config),
			preview_url=COALESCE($10, preview_url),
			updated_at=NOW()
		WHERE id=$1 AND status=$2`,
		id, cur.Status, status, CommitStateFor(status), p.ImageRef, p.Error, build, p.GitSHA, cfg, preview)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 0 {
		return nil, false, ErrBadTransition // lost a race with another update
	}
	d, err = s.GetByID(ctx, id)
	return d, changed, err
}

// SupersedeOlder marks older live production deployments of the project as superseded.
func (s *Store) SupersedeOlder(ctx context.Context, d *Deployment) error {
	_, err := s.pool.Exec(ctx, `UPDATE deployments SET status='superseded', updated_at=NOW()
		WHERE org_id=$1 AND project_id=$2 AND NOT is_preview AND status='ready' AND id <> $3 AND created_at < $4`,
		d.OrgID, d.ProjectID, d.ID, d.CreatedAt)
	return err
}

// ActivePreviews returns live preview deployments of the same PR (or branch when no PR).
func (s *Store) ActivePreviews(ctx context.Context, orgID, projectID string, pr int, branch string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments
		WHERE org_id=$1 AND project_id=$2 AND is_preview AND status IN ('queued','building','deploying','ready')
		  AND (($3 > 0 AND pr_number=$3) OR ($3 = 0 AND pr_number=0 AND git_branch=$4))
		ORDER BY created_at DESC`, orgID, projectID, pr, branch)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// PreviewsByPR finds live previews for a PR across the projects connected to fullName.
func (s *Store) PreviewsByPR(ctx context.Context, orgID, projectID, fullName string, pr int) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments
		WHERE org_id=$1 AND project_id=$2 AND is_preview AND pr_number=$3 AND ($4='' OR full_name=$4)
		  AND status IN ('queued','building','deploying','ready')`, orgID, projectID, pr, fullName)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// ExpiredPreviews returns live previews older than cutoff.
func (s *Store) ExpiredPreviews(ctx context.Context, cutoff time.Time) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments
		WHERE is_preview AND created_at < $1 AND status IN ('queued','building','deploying','ready') LIMIT 200`, cutoff)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}

// DeleteProject removes (soft: marks) all deployments of a project.
func (s *Store) DeleteProject(ctx context.Context, projectID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deployments WHERE project_id=$1`, projectID)
	return tag.RowsAffected(), err
}

// LiveByProject returns deployments that may still own resources.
func (s *Store) LiveByProject(ctx context.Context, projectID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+depCols+` FROM deployments WHERE project_id=$1 AND status IN ('queued','building','deploying','ready','superseded')`, projectID)
	if err != nil {
		return nil, err
	}
	return scanRows(rows)
}
