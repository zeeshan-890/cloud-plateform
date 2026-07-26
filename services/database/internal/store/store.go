package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

type Database struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	ProjectID      string    `json:"project_id"`
	Name           string    `json:"name"`
	Mode           string    `json:"mode"`
	Status         string    `json:"status"`
	SchemaName     string    `json:"schema_name"`
	RoleName       string    `json:"role_name"`
	SecretRef      string    `json:"secret_ref"`
	ConnectionHint string    `json:"connection_hint"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, d *Database) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.Status == "" {
		d.Status = "ready"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO managed_databases (
			id, org_id, project_id, name, mode, status, schema_name, role_name,
			secret_ref, connection_hint, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	`, d.ID, d.OrgID, d.ProjectID, d.Name, d.Mode, d.Status, d.SchemaName, d.RoleName,
		d.SecretRef, d.ConnectionHint, d.CreatedBy, d.CreatedAt, d.UpdatedAt)
	if err != nil && (strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate")) {
		return ErrConflict
	}
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Database, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, name, mode, status, schema_name, role_name,
		       secret_ref, connection_hint, created_by, created_at, updated_at
		FROM managed_databases WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Database{}
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.Name, &d.Mode, &d.Status, &d.SchemaName, &d.RoleName,
			&d.SecretRef, &d.ConnectionHint, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Database, error) {
	d := &Database{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, name, mode, status, schema_name, role_name,
		       secret_ref, connection_hint, created_by, created_at, updated_at
		FROM managed_databases WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID).Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.Name, &d.Mode, &d.Status, &d.SchemaName, &d.RoleName,
		&d.SecretRef, &d.ConnectionHint, &d.CreatedBy, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) Delete(ctx context.Context, orgID, projectID, id string) (*Database, error) {
	d, err := s.Get(ctx, orgID, projectID, id)
	if err != nil {
		return nil, err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM managed_databases WHERE id=$1`, id)
	return d, err
}
