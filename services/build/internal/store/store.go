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
	// ErrTerminal means the build already finished with a different state.
	ErrTerminal = errors.New("build already in a terminal state")
)

// MaxLogBytes caps the stored log (the newest bytes are kept).
const MaxLogBytes = 1 << 20

// Build is one build record. It never carries a clone URL or any credential.
type Build struct {
	ID            string          `json:"id"`
	OrgID         string          `json:"org_id"`
	ProjectID     string          `json:"project_id"`
	DeploymentID  *string         `json:"deployment_id,omitempty"`
	RepoID        *string         `json:"repo_id,omitempty"`
	PRNumber      int             `json:"pr_number,omitempty"`
	Status        string          `json:"status"`
	GitSHA        string          `json:"git_sha"`
	GitBranch     string          `json:"git_branch"`
	FullName      string          `json:"full_name"`
	Framework     string          `json:"framework"`
	ImageRef      string          `json:"image_ref"`
	Logs          string          `json:"logs,omitempty"`
	LogsTruncated bool            `json:"logs_truncated,omitempty"`
	Error         string          `json:"error,omitempty"`
	ScanStatus    string          `json:"scan_status"`
	Scan          json.RawMessage `json:"scan,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	StartedAt     *time.Time      `json:"started_at,omitempty"`
	FinishedAt    *time.Time      `json:"finished_at,omitempty"`
}

// Update is a status patch from the worker. Logs is a chunk to APPEND.
type Update struct {
	Status     string
	Framework  string
	ImageRef   string
	LogsAppend string
	Error      string
	ScanStatus string
	Scan       json.RawMessage
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, org_id, project_id, deployment_id, repo_id, pr_number, status, git_sha, git_branch, full_name,
	framework, image_ref, logs, logs_truncated, error, scan_status, scan, created_at, updated_at, started_at, finished_at`

func scan(row pgx.Row, withLogs bool) (*Build, error) {
	b := &Build{}
	var sc []byte
	err := row.Scan(&b.ID, &b.OrgID, &b.ProjectID, &b.DeploymentID, &b.RepoID, &b.PRNumber, &b.Status, &b.GitSHA, &b.GitBranch, &b.FullName,
		&b.Framework, &b.ImageRef, &b.Logs, &b.LogsTruncated, &b.Error, &b.ScanStatus, &sc, &b.CreatedAt, &b.UpdatedAt, &b.StartedAt, &b.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if len(sc) > 0 {
		b.Scan = sc
	}
	if !withLogs {
		b.Logs = ""
	}
	return b, err
}

func (s *Store) Create(ctx context.Context, b *Build) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	b.CreatedAt, b.UpdatedAt = now, now
	if b.Status == "" {
		b.Status = "queued"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO builds (id, org_id, project_id, deployment_id, repo_id, pr_number, status, git_sha, git_branch, clone_url, full_name,
		                    framework, image_ref, logs, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'',$10,$11,$12,$13,$14,$15,$16)
	`, b.ID, b.OrgID, b.ProjectID, b.DeploymentID, b.RepoID, b.PRNumber, b.Status, b.GitSHA, b.GitBranch, b.FullName,
		b.Framework, b.ImageRef, b.Logs, b.Error, b.CreatedAt, b.UpdatedAt)
	return err
}

// List returns recent builds without their logs.
func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Build, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM builds WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Build{}
	for rows.Next() {
		b, err := scan(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, id string) (*Build, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM builds WHERE id=$1`, id), true)
}

// Apply patches a build. Terminal states (succeeded/failed/canceled) are final: a
// repeated identical terminal update is accepted (idempotent), any other change returns
// ErrTerminal. changed reports whether this call performed the transition to terminal.
func (s *Store) Apply(ctx context.Context, id string, u Update) (b *Build, changed bool, err error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, false, err
	}
	if IsTerminal(cur.Status) {
		if u.Status == cur.Status || u.Status == "" {
			return cur, false, nil
		}
		return cur, false, ErrTerminal
	}
	if !ValidTransition(cur.Status, u.Status) {
		return cur, false, ErrTerminal
	}
	now := time.Now().UTC()
	terminal := IsTerminal(u.Status)
	started := u.Status == "running"
	tag, err := s.pool.Exec(ctx, `
		UPDATE builds SET
			status = COALESCE(NULLIF($2,''), status),
			framework = COALESCE(NULLIF($3,''), framework),
			image_ref = COALESCE(NULLIF($4,''), image_ref),
			logs_truncated = logs_truncated OR (length(logs) + length($5) > $11),
			logs = CASE WHEN $5 = '' THEN logs ELSE right(logs || $5, $11) END,
			error = COALESCE(NULLIF($6,''), error),
			scan_status = COALESCE(NULLIF($7,''), scan_status),
			scan = COALESCE($8::jsonb, scan),
			updated_at = $9,
			started_at = CASE WHEN $10 THEN COALESCE(started_at, $9) ELSE started_at END,
			finished_at = CASE WHEN $12 THEN $9 ELSE finished_at END
		WHERE id=$1 AND status NOT IN ('succeeded','failed','canceled')
	`, id, u.Status, u.Framework, u.ImageRef, u.LogsAppend, u.Error, u.ScanStatus, nullJSON(u.Scan), now, started, MaxLogBytes, terminal)
	if err != nil {
		return nil, false, err
	}
	if tag.RowsAffected() == 0 {
		cur, _ = s.Get(ctx, id)
		return cur, false, nil
	}
	b, err = s.Get(ctx, id)
	return b, terminal, err
}

func nullJSON(j json.RawMessage) any {
	if len(j) == 0 {
		return nil
	}
	return string(j)
}

// IsTerminal reports final build states.
func IsTerminal(s string) bool { return s == "succeeded" || s == "failed" || s == "canceled" }

// ValidTransition is the build state machine: queued -> running -> terminal, or
// queued -> terminal (cancel / enqueue failure).
func ValidTransition(from, to string) bool {
	if to == "" || from == to {
		return true
	}
	switch from {
	case "queued":
		return to == "running" || IsTerminal(to)
	case "running":
		return IsTerminal(to)
	}
	return false
}

// DeleteProject removes every build of a project (project delete cascade).
func (s *Store) DeleteProject(ctx context.Context, projectID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM builds WHERE project_id=$1`, projectID)
	return tag.RowsAffected(), err
}
