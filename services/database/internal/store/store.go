package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

const (
	StatusProvisioning = "provisioning"
	StatusReady        = "ready"
	StatusFailed       = "failed"
)

type Database struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	ProjectID      string    `json:"project_id"`
	Name           string    `json:"name"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	AddonID        *string   `json:"addon_id,omitempty"`
	SchemaName     string    `json:"-"`
	RoleName       string    `json:"-"`
	SecretRef      string    `json:"secret_ref"`
	ConnectionHint string    `json:"connection_hint"`
	Error          string    `json:"error,omitempty"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Store is the persistence contract.
type Store interface {
	Create(ctx context.Context, d *Database) error
	Update(ctx context.Context, d *Database) error
	List(ctx context.Context, orgID, projectID string) ([]Database, error)
	ListByProject(ctx context.Context, projectID string) ([]Database, error)
	Get(ctx context.Context, orgID, projectID, id string) (*Database, error)
	Delete(ctx context.Context, id string) error
	LegacyRoles(ctx context.Context) ([]string, error)
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

const cols = `id, org_id, project_id, name, mode, status, addon_id::text, schema_name, role_name, secret_ref, connection_hint, error, created_by, created_at, updated_at`

func scan(row pgx.Row, d *Database) error {
	return row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.Name, &d.Mode, &d.Status, &d.AddonID, &d.SchemaName, &d.RoleName,
		&d.SecretRef, &d.ConnectionHint, &d.Error, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
}

func (s *PG) Create(ctx context.Context, d *Database) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	if d.Status == "" {
		d.Status = StatusProvisioning
	}
	if d.Mode == "" {
		d.Mode = "container"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO managed_databases (id, org_id, project_id, name, mode, status, addon_id, secret_ref, connection_hint, error, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7::uuid,$8,$9,$10,$11,$12,$13)`,
		d.ID, d.OrgID, d.ProjectID, d.Name, d.Mode, d.Status, d.AddonID, d.SecretRef, d.ConnectionHint, d.Error, d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrConflict
	}
	return err
}

func (s *PG) Update(ctx context.Context, d *Database) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `UPDATE managed_databases SET status=$2, addon_id=$3::uuid, secret_ref=$4, connection_hint=$5, error=$6, updated_at=$7 WHERE id=$1`,
		d.ID, d.Status, d.AddonID, d.SecretRef, d.ConnectionHint, d.Error, d.UpdatedAt)
	return err
}

func (s *PG) list(ctx context.Context, sql string, args ...any) ([]Database, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := scan(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *PG) List(ctx context.Context, orgID, projectID string) ([]Database, error) {
	return s.list(ctx, `SELECT `+cols+` FROM managed_databases WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC`, orgID, projectID)
}

func (s *PG) ListByProject(ctx context.Context, projectID string) ([]Database, error) {
	return s.list(ctx, `SELECT `+cols+` FROM managed_databases WHERE project_id=$1::uuid`, projectID)
}

func (s *PG) Get(ctx context.Context, orgID, projectID, id string) (*Database, error) {
	d := &Database{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM managed_databases WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID), d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *PG) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM managed_databases WHERE id=$1`, id)
	return err
}

// LegacyRoles returns login roles created by the old shared-schema implementation.
func (s *PG) LegacyRoles(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT role_name FROM managed_databases WHERE role_name <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
