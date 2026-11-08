// Package store persists runtime instance and job-run state. It deliberately owns only
// the runtime_* tables: secrets, projects, domains and add-ons are other services' data
// and are reached through their internal APIs, never through SQL.
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

// Instance is one managed container (app replica or add-on).
type Instance struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	DeploymentID  *string    `json:"deployment_id,omitempty"`
	Kind          string     `json:"kind"`
	Role          string     `json:"role"` // app | addon
	ImageRef      string     `json:"image_ref"`
	Status        string     `json:"status"`
	DesiredState  string     `json:"desired_state"`
	ContainerID   string     `json:"container_id"`
	ContainerName string     `json:"container_name"`
	Slot          string     `json:"slot"`
	Port          int        `json:"port"`
	RestartPolicy string     `json:"restart_policy"`
	Mode          string     `json:"mode"`
	IsPreview     bool       `json:"is_preview"`
	Service       string     `json:"service"`
	Color         string     `json:"color,omitempty"`
	Network       string     `json:"network"`
	HealthPath    string     `json:"health_path"`
	Replica       int        `json:"replica"`
	RestartCount  int        `json:"restart_count"`
	LastRestartAt *time.Time `json:"last_restart_at,omitempty"`
	ExitCode      *int       `json:"exit_code,omitempty"`
	Error         string     `json:"error,omitempty"`
	HealthStatus  string     `json:"health_status"`
	LastHealthAt  *time.Time `json:"last_health_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	// NodeID is the server the container runs on ("" = the control plane itself); HostPort is the published
	// port on that node for apps on remote nodes.
	NodeID   string `json:"node_id"`
	HostPort int    `json:"host_port,omitempty"`
}

// JobRun is the record of one one-off container execution.
type JobRun struct {
	ID             string     `json:"id"`
	OrgID          string     `json:"org_id"`
	ProjectID      string     `json:"project_id"`
	CronID         string     `json:"cron_id,omitempty"`
	Name           string     `json:"name"`
	ImageRef       string     `json:"image_ref"`
	Command        []string   `json:"command"`
	ContainerName  string     `json:"container_name"`
	Status         string     `json:"status"` // running | succeeded | failed | timeout
	ExitCode       *int       `json:"exit_code,omitempty"`
	Error          string     `json:"error,omitempty"`
	LogTail        string     `json:"log_tail,omitempty"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	StartedAt      time.Time  `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const instCols = `id, org_id, project_id, deployment_id, kind, role, image_ref, status, desired_state,
	container_id, container_name, slot, port, restart_policy, mode, COALESCE(is_preview,false), service, color, network,
	health_path, replica, restart_count, last_restart_at, exit_code, error, health_status, last_health_at, created_at, updated_at,
	node_id, host_port`

func scanInst(row pgx.Row) (*Instance, error) {
	in := &Instance{}
	err := row.Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.Role, &in.ImageRef, &in.Status, &in.DesiredState,
		&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.IsPreview, &in.Service, &in.Color, &in.Network,
		&in.HealthPath, &in.Replica, &in.RestartCount, &in.LastRestartAt, &in.ExitCode, &in.Error, &in.HealthStatus, &in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt, &in.NodeID, &in.HostPort)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func scanInstRows(rows pgx.Rows) ([]Instance, error) {
	defer rows.Close()
	out := []Instance{}
	for rows.Next() {
		in, err := scanInst(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *in)
	}
	return out, rows.Err()
}

// Create inserts a new instance row (defaults are filled in).
func (s *Store) Create(ctx context.Context, in *Instance) error {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	in.CreatedAt, in.UpdatedAt = now, now
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&in.Kind, "container")
	def(&in.Role, "app")
	def(&in.Status, "desired")
	def(&in.DesiredState, "running")
	def(&in.Slot, "node-1")
	def(&in.RestartPolicy, "on-failure")
	def(&in.HealthStatus, "unknown")
	def(&in.HealthPath, "/")
	def(&in.Mode, "docker")
	if in.Port == 0 {
		in.Port = 8080
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_instances (
			id, org_id, project_id, deployment_id, kind, role, image_ref, status, desired_state,
			container_id, container_name, slot, port, restart_policy, mode, is_preview, service, color, network,
			health_path, replica, restart_count, last_restart_at, exit_code, error, health_status, last_health_at, created_at, updated_at,
			node_id, host_port
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31)
	`, in.ID, in.OrgID, in.ProjectID, in.DeploymentID, in.Kind, in.Role, in.ImageRef, in.Status, in.DesiredState,
		in.ContainerID, in.ContainerName, in.Slot, in.Port, in.RestartPolicy, in.Mode, in.IsPreview, in.Service, in.Color, in.Network,
		in.HealthPath, in.Replica, in.RestartCount, in.LastRestartAt, in.ExitCode, in.Error, in.HealthStatus, in.LastHealthAt, in.CreatedAt, in.UpdatedAt,
		in.NodeID, in.HostPort)
	return err
}

// Update writes all mutable fields.
func (s *Store) Update(ctx context.Context, in *Instance) error {
	in.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE runtime_instances SET
			status=$2, desired_state=$3, container_id=$4, container_name=$5, image_ref=$6,
			deployment_id=$7, error=$8, health_status=$9, last_health_at=$10, mode=$11,
			restart_policy=$12, slot=$13, port=$14, is_preview=$15, service=$16, color=$17, network=$18,
			health_path=$19, replica=$20, restart_count=$21, last_restart_at=$22, exit_code=$23, updated_at=$24,
			node_id=$25, host_port=$26
		WHERE id=$1
	`, in.ID, in.Status, in.DesiredState, in.ContainerID, in.ContainerName, in.ImageRef,
		in.DeploymentID, in.Error, in.HealthStatus, in.LastHealthAt, in.Mode,
		in.RestartPolicy, in.Slot, in.Port, in.IsPreview, in.Service, in.Color, in.Network,
		in.HealthPath, in.Replica, in.RestartCount, in.LastRestartAt, in.ExitCode, in.UpdatedAt, in.NodeID, in.HostPort)
	return err
}

// Delete removes an instance row.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM runtime_instances WHERE id=$1`, id)
	return err
}

// List returns the most recent instances of a project.
func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// Get returns an instance scoped to org and project.
func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID))
}

// GetByID returns an instance by id only (internal callers).
func (s *Store) GetByID(ctx context.Context, id string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `SELECT `+instCols+` FROM runtime_instances WHERE id=$1`, id))
}

// ListActiveProduction returns the non-preview app instances that are (or should be)
// serving production traffic, newest first.
func (s *Store) ListActiveProduction(ctx context.Context, orgID, projectID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE org_id=$1 AND project_id=$2 AND role='app' AND desired_state='running'
		  AND COALESCE(is_preview,false)=false AND status IN ('running','starting','unhealthy')
		ORDER BY created_at DESC`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// FindByDeploymentID returns the instances created for a deployment.
func (s *Store) FindByDeploymentID(ctx context.Context, deploymentID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE deployment_id=$1::uuid ORDER BY replica ASC, created_at DESC`, deploymentID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// ListByProject returns every instance of a project (used by delete cascade).
func (s *Store) ListByProject(ctx context.Context, projectID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances WHERE project_id=$1`, projectID)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// DeleteByProject removes all rows of a project (instances and job runs).
func (s *Store) DeleteByProject(ctx context.Context, projectID string) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM runtime_instances WHERE project_id=$1`, projectID); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM runtime_job_runs WHERE project_id=$1`, projectID)
	return err
}

// ListDesiredRunning returns every instance that should be running.
func (s *Store) ListDesiredRunning(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE desired_state='running' AND status NOT IN ('starting') ORDER BY created_at LIMIT 2000`)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// ListAllContainers returns (container_id) for every row with a container (reconcile).
func (s *Store) ListAllWithContainer(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+instCols+` FROM runtime_instances WHERE container_id <> '' LIMIT 5000`)
	if err != nil {
		return nil, err
	}
	return scanInstRows(rows)
}

// ---------------------------------------------------------------- job runs

const jobCols = `id, org_id, project_id, cron_id, name, image_ref, command, container_name, status, exit_code, error, log_tail,
	timeout_seconds, started_at, finished_at`

func scanJob(row pgx.Row) (*JobRun, error) {
	j := &JobRun{}
	err := row.Scan(&j.ID, &j.OrgID, &j.ProjectID, &j.CronID, &j.Name, &j.ImageRef, &j.Command, &j.ContainerName, &j.Status,
		&j.ExitCode, &j.Error, &j.LogTail, &j.TimeoutSeconds, &j.StartedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if j.Command == nil {
		j.Command = []string{}
	}
	return j, err
}

// CreateJob inserts a new run.
func (s *Store) CreateJob(ctx context.Context, j *JobRun) error {
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	if j.StartedAt.IsZero() {
		j.StartedAt = time.Now().UTC()
	}
	if j.Status == "" {
		j.Status = "running"
	}
	if j.Command == nil {
		j.Command = []string{}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO runtime_job_runs
		(id, org_id, project_id, cron_id, name, image_ref, command, container_name, status, exit_code, error, log_tail, timeout_seconds, started_at, finished_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		j.ID, j.OrgID, j.ProjectID, j.CronID, j.Name, j.ImageRef, j.Command, j.ContainerName, j.Status, j.ExitCode, j.Error, j.LogTail,
		j.TimeoutSeconds, j.StartedAt, j.FinishedAt)
	return err
}

// FinishJob records the outcome of a run.
func (s *Store) FinishJob(ctx context.Context, j *JobRun) error {
	now := time.Now().UTC()
	j.FinishedAt = &now
	_, err := s.pool.Exec(ctx, `UPDATE runtime_job_runs SET status=$2, exit_code=$3, error=$4, log_tail=$5, finished_at=$6 WHERE id=$1`,
		j.ID, j.Status, j.ExitCode, j.Error, j.LogTail, j.FinishedAt)
	return err
}

// ListJobs returns recent runs of a project.
func (s *Store) ListJobs(ctx context.Context, orgID, projectID string, limit int) ([]JobRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+jobCols+` FROM runtime_job_runs
		WHERE org_id=$1 AND project_id=$2 ORDER BY started_at DESC LIMIT $3`, orgID, projectID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JobRun{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

// GetJob returns one run scoped to the project.
func (s *Store) GetJob(ctx context.Context, orgID, projectID, id string) (*JobRun, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM runtime_job_runs WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID))
}

// FailStaleJobs marks runs still 'running' after the platform restarted as failed.
func (s *Store) FailStaleJobs(ctx context.Context, olderThan time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE runtime_job_runs SET status='failed', error='runtime restarted while job was running', finished_at=NOW()
		WHERE status='running' AND started_at < $1`, olderThan)
	return tag.RowsAffected(), err
}

// GetByContainerID returns the instance owning a docker container id (or name).
func (s *Store) GetByContainerID(ctx context.Context, containerID string) (*Instance, error) {
	return scanInst(s.pool.QueryRow(ctx, `SELECT `+instCols+` FROM runtime_instances
		WHERE container_id=$1 OR container_name=$1 ORDER BY created_at DESC LIMIT 1`, containerID))
}
