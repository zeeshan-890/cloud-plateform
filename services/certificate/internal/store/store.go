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

type Certificate struct {
	ID              string          `json:"id"`
	OrgID           string          `json:"org_id"`
	ProjectID       string          `json:"project_id"`
	DomainID        *string         `json:"domain_id,omitempty"`
	Hostname        string          `json:"hostname"`
	Status          string          `json:"status"`
	Provider        string          `json:"provider"`
	Resolver        string          `json:"resolver"`
	IssuedAt        *time.Time      `json:"issued_at,omitempty"`
	ExpiresAt       *time.Time      `json:"expires_at,omitempty"`
	RenewedAt       *time.Time      `json:"renewed_at,omitempty"`
	RenewBeforeDays int             `json:"renew_before_days"`
	Error           string          `json:"error,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, c *Certificate) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	if c.Status == "" {
		c.Status = "pending"
	}
	if c.Provider == "" {
		c.Provider = "traefik-acme"
	}
	if c.RenewBeforeDays == 0 {
		c.RenewBeforeDays = 30
	}
	if len(c.Metadata) == 0 {
		c.Metadata = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO certificates (
			id, org_id, project_id, domain_id, hostname, status, provider, resolver,
			issued_at, expires_at, renewed_at, renew_before_days, error, metadata, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
	`, c.ID, c.OrgID, c.ProjectID, c.DomainID, c.Hostname, c.Status, c.Provider, c.Resolver,
		c.IssuedAt, c.ExpiresAt, c.RenewedAt, c.RenewBeforeDays, c.Error, c.Metadata, c.CreatedAt, c.UpdatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, domain_id, hostname, status, provider, resolver,
		       issued_at, expires_at, renewed_at, renew_before_days, error, metadata, created_at, updated_at
		FROM certificates WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Certificate
	for rows.Next() {
		var c Certificate
		if err := rows.Scan(&c.ID, &c.OrgID, &c.ProjectID, &c.DomainID, &c.Hostname, &c.Status, &c.Provider, &c.Resolver,
			&c.IssuedAt, &c.ExpiresAt, &c.RenewedAt, &c.RenewBeforeDays, &c.Error, &c.Metadata, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Certificate{}
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Certificate, error) {
	c := &Certificate{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, domain_id, hostname, status, provider, resolver,
		       issued_at, expires_at, renewed_at, renew_before_days, error, metadata, created_at, updated_at
		FROM certificates WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID).Scan(&c.ID, &c.OrgID, &c.ProjectID, &c.DomainID, &c.Hostname, &c.Status, &c.Provider, &c.Resolver,
		&c.IssuedAt, &c.ExpiresAt, &c.RenewedAt, &c.RenewBeforeDays, &c.Error, &c.Metadata, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) GetByID(ctx context.Context, id string) (*Certificate, error) {
	c := &Certificate{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, domain_id, hostname, status, provider, resolver,
		       issued_at, expires_at, renewed_at, renew_before_days, error, metadata, created_at, updated_at
		FROM certificates WHERE id=$1
	`, id).Scan(&c.ID, &c.OrgID, &c.ProjectID, &c.DomainID, &c.Hostname, &c.Status, &c.Provider, &c.Resolver,
		&c.IssuedAt, &c.ExpiresAt, &c.RenewedAt, &c.RenewBeforeDays, &c.Error, &c.Metadata, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *Store) ListExpiring(ctx context.Context) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, domain_id, hostname, status, provider, resolver,
		       issued_at, expires_at, renewed_at, renew_before_days, error, metadata, created_at, updated_at
		FROM certificates
		WHERE status IN ('issued','renewing') AND expires_at IS NOT NULL
		  AND expires_at <= NOW() + (renew_before_days || ' days')::interval
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Certificate
	for rows.Next() {
		var c Certificate
		if err := rows.Scan(&c.ID, &c.OrgID, &c.ProjectID, &c.DomainID, &c.Hostname, &c.Status, &c.Provider, &c.Resolver,
			&c.IssuedAt, &c.ExpiresAt, &c.RenewedAt, &c.RenewBeforeDays, &c.Error, &c.Metadata, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []Certificate{}
	}
	return out, rows.Err()
}

func (s *Store) Update(ctx context.Context, c *Certificate) error {
	c.UpdatedAt = time.Now().UTC()
	if len(c.Metadata) == 0 {
		c.Metadata = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE certificates SET status=$2, issued_at=$3, expires_at=$4, renewed_at=$5,
			error=$6, metadata=$7, resolver=$8, updated_at=$9
		WHERE id=$1
	`, c.ID, c.Status, c.IssuedAt, c.ExpiresAt, c.RenewedAt, c.Error, c.Metadata, c.Resolver, c.UpdatedAt)
	return err
}
