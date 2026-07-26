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

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Domain struct {
	ID               string     `json:"id"`
	OrgID            string     `json:"org_id"`
	ProjectID        string     `json:"project_id"`
	DeploymentID     *string    `json:"deployment_id,omitempty"`
	Hostname         string     `json:"hostname"`
	Status           string     `json:"status"`
	VerificationType string     `json:"verification_type"`
	VerificationToken string    `json:"verification_token"`
	VerifiedAt       *time.Time `json:"verified_at,omitempty"`
	ForceVerified    bool       `json:"force_verified"`
	TraefikFile      string     `json:"traefik_file,omitempty"`
	CertificateID    *string    `json:"certificate_id,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, d *Domain) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.Status == "" {
		d.Status = "pending"
	}
	if d.VerificationType == "" {
		d.VerificationType = "cname"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO domains (
			id, org_id, project_id, deployment_id, hostname, status, verification_type,
			verification_token, verified_at, force_verified, traefik_file, certificate_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	`, d.ID, d.OrgID, d.ProjectID, d.DeploymentID, d.Hostname, d.Status, d.VerificationType,
		d.VerificationToken, d.VerifiedAt, d.ForceVerified, d.TraefikFile, d.CertificateID, d.CreatedAt, d.UpdatedAt)
	if err != nil && (strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique")) {
		return ErrConflict
	}
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Domain, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, deployment_id, hostname, status, verification_type,
		       verification_token, verified_at, force_verified, traefik_file, certificate_id, created_at, updated_at
		FROM domains WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.DeploymentID, &d.Hostname, &d.Status, &d.VerificationType,
			&d.VerificationToken, &d.VerifiedAt, &d.ForceVerified, &d.TraefikFile, &d.CertificateID, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	if out == nil {
		out = []Domain{}
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID, id string) (*Domain, error) {
	d := &Domain{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, deployment_id, hostname, status, verification_type,
		       verification_token, verified_at, force_verified, traefik_file, certificate_id, created_at, updated_at
		FROM domains WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, id, orgID, projectID).Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.DeploymentID, &d.Hostname, &d.Status, &d.VerificationType,
		&d.VerificationToken, &d.VerifiedAt, &d.ForceVerified, &d.TraefikFile, &d.CertificateID, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *Store) Update(ctx context.Context, d *Domain) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE domains SET status=$2, verified_at=$3, force_verified=$4, traefik_file=$5,
			certificate_id=$6, deployment_id=$7, updated_at=$8
		WHERE id=$1
	`, d.ID, d.Status, d.VerifiedAt, d.ForceVerified, d.TraefikFile, d.CertificateID, d.DeploymentID, d.UpdatedAt)
	return err
}

func (s *Store) Delete(ctx context.Context, orgID, projectID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
