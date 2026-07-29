package store

import (
	"context"
	"encoding/json"
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

type Addon struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	ProjectID      string          `json:"project_id"`
	Engine         string          `json:"engine"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	Endpoint       string          `json:"endpoint"`
	Metadata       json.RawMessage `json:"metadata"`
	SecretRef      string          `json:"secret_ref"`
	ConnectionHint string          `json:"connection_hint"`
	CreatedBy      *string         `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, a *Addon) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	if a.Status == "" {
		a.Status = "ready"
	}
	if len(a.Metadata) == 0 {
		a.Metadata = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO managed_addons (
			id, org_id, project_id, engine, name, mode, status, endpoint, metadata,
			secret_ref, connection_hint, created_by, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, a.ID, a.OrgID, a.ProjectID, a.Engine, a.Name, a.Mode, a.Status, a.Endpoint, a.Metadata,
		a.SecretRef, a.ConnectionHint, a.CreatedBy, a.CreatedAt, a.UpdatedAt)
	if err != nil && (strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate")) {
		return ErrConflict
	}
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID, engine string) ([]Addon, error) {
	q := `
		SELECT id, org_id, project_id, engine, name, mode, status, endpoint, metadata,
		       secret_ref, connection_hint, created_by, created_at, updated_at
		FROM managed_addons WHERE org_id=$1 AND project_id=$2`
	args := []any{orgID, projectID}
	if engine != "" {
		q += ` AND engine=$3`
		args = append(args, engine)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Addon{}
	for rows.Next() {
		var a Addon
		if err := rows.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.Engine, &a.Name, &a.Mode, &a.Status, &a.Endpoint, &a.Metadata,
			&a.SecretRef, &a.ConnectionHint, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Addon, error) {
	a := &Addon{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, engine, name, mode, status, endpoint, metadata,
		       secret_ref, connection_hint, created_by, created_at, updated_at
		FROM managed_addons WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID).Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.Engine, &a.Name, &a.Mode, &a.Status, &a.Endpoint, &a.Metadata,
		&a.SecretRef, &a.ConnectionHint, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *Store) Delete(ctx context.Context, orgID, projectID, id string) (*Addon, error) {
	a, err := s.Get(ctx, orgID, projectID, id)
	if err != nil {
		return nil, err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM managed_addons WHERE id=$1`, id)
	return a, err
}
