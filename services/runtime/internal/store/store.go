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

type Instance struct {
	ID            string     `json:"id"`
	OrgID         string     `json:"org_id"`
	ProjectID     string     `json:"project_id"`
	DeploymentID  *string    `json:"deployment_id,omitempty"`
	Kind          string     `json:"kind"`
	ImageRef      string     `json:"image_ref"`
	Status        string     `json:"status"`
	DesiredState  string     `json:"desired_state"`
	ContainerID   string     `json:"container_id"`
	ContainerName string     `json:"container_name"`
	Slot          string     `json:"slot"`
	Port          int        `json:"port"`
	RestartPolicy string     `json:"restart_policy"`
	Mode          string     `json:"mode"`
	Error         string     `json:"error,omitempty"`
	HealthStatus  string     `json:"health_status"`
	LastHealthAt  *time.Time `json:"last_health_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, in *Instance) error {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	if in.Kind == "" {
		in.Kind = "container"
	}
	if in.Status == "" {
		in.Status = "desired"
	}
	if in.DesiredState == "" {
		in.DesiredState = "running"
	}
	if in.Slot == "" {
		in.Slot = "node-1"
	}
	if in.Port == 0 {
		in.Port = 8080
	}
	if in.RestartPolicy == "" {
		in.RestartPolicy = "on-failure"
	}
	if in.HealthStatus == "" {
		in.HealthStatus = "unknown"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_instances (
			id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
			container_id, container_name, slot, port, restart_policy, mode, error, health_status,
			last_health_at, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
	`, in.ID, in.OrgID, in.ProjectID, in.DeploymentID, in.Kind, in.ImageRef, in.Status, in.DesiredState,
		in.ContainerID, in.ContainerName, in.Slot, in.Port, in.RestartPolicy, in.Mode, in.Error, in.HealthStatus,
		in.LastHealthAt, in.CreatedAt, in.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, error, health_status,
		       last_health_at, created_at, updated_at
		FROM runtime_instances WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
			&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.Error, &in.HealthStatus,
			&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	if out == nil {
		out = []Instance{}
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Instance, error) {
	in := &Instance{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, error, health_status,
		       last_health_at, created_at, updated_at
		FROM runtime_instances WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID).Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
		&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.Error, &in.HealthStatus,
		&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func (s *Store) GetByID(ctx context.Context, id string) (*Instance, error) {
	in := &Instance{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, error, health_status,
		       last_health_at, created_at, updated_at
		FROM runtime_instances WHERE id=$1
	`, id).Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
		&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.Error, &in.HealthStatus,
		&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func (s *Store) FindActiveByProject(ctx context.Context, orgID, projectID string) (*Instance, error) {
	in := &Instance{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, error, health_status,
		       last_health_at, created_at, updated_at
		FROM runtime_instances
		WHERE org_id=$1 AND project_id=$2 AND desired_state='running'
		ORDER BY created_at DESC LIMIT 1
	`, orgID, projectID).Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
		&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.Error, &in.HealthStatus,
		&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return in, err
}

func (s *Store) ListDesiredRunning(ctx context.Context) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, deployment_id, kind, image_ref, status, desired_state,
		       container_id, container_name, slot, port, restart_policy, mode, error, health_status,
		       last_health_at, created_at, updated_at
		FROM runtime_instances WHERE desired_state='running'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Instance
	for rows.Next() {
		var in Instance
		if err := rows.Scan(&in.ID, &in.OrgID, &in.ProjectID, &in.DeploymentID, &in.Kind, &in.ImageRef, &in.Status, &in.DesiredState,
			&in.ContainerID, &in.ContainerName, &in.Slot, &in.Port, &in.RestartPolicy, &in.Mode, &in.Error, &in.HealthStatus,
			&in.LastHealthAt, &in.CreatedAt, &in.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	if out == nil {
		out = []Instance{}
	}
	return out, rows.Err()
}

func (s *Store) Update(ctx context.Context, in *Instance) error {
	in.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE runtime_instances SET
			status=$2, desired_state=$3, container_id=$4, container_name=$5, image_ref=$6,
			deployment_id=$7, error=$8, health_status=$9, last_health_at=$10, mode=$11,
			restart_policy=$12, slot=$13, port=$14, updated_at=$15
		WHERE id=$1
	`, in.ID, in.Status, in.DesiredState, in.ContainerID, in.ContainerName, in.ImageRef,
		in.DeploymentID, in.Error, in.HealthStatus, in.LastHealthAt, in.Mode,
		in.RestartPolicy, in.Slot, in.Port, in.UpdatedAt)
	return err
}
