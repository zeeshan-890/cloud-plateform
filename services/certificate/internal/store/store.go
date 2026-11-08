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
	ID                 string          `json:"id"`
	OrgID              string          `json:"org_id"`
	ProjectID          string          `json:"project_id"`
	DomainID           *string         `json:"domain_id,omitempty"`
	Hostname           string          `json:"hostname"`
	Status             string          `json:"status"`
	Provider           string          `json:"provider"`
	Resolver           string          `json:"resolver"`
	Issuer             string          `json:"issuer,omitempty"`
	Serial             string          `json:"serial,omitempty"`
	SANs               json.RawMessage `json:"sans"`
	IssuedAt           *time.Time      `json:"issued_at,omitempty"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty"`
	RenewedAt          *time.Time      `json:"renewed_at,omitempty"`
	LastProbedAt       *time.Time      `json:"last_probed_at,omitempty"`
	ReissueRequestedAt *time.Time      `json:"reissue_requested_at,omitempty"`
	RenewBeforeDays    int             `json:"renew_before_days"`
	Error              string          `json:"error,omitempty"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

// Store is the persistence contract (implemented by *PG and test fakes).
type Store interface {
	// Upsert creates the certificate for a domain or returns the existing one.
	Upsert(ctx context.Context, c *Certificate) (*Certificate, error)
	List(ctx context.Context, orgID, projectID string) ([]Certificate, error)
	ListByProject(ctx context.Context, projectID string) ([]Certificate, error)
	ListAll(ctx context.Context) ([]Certificate, error)
	Get(ctx context.Context, orgID, projectID, id string) (*Certificate, error)
	Update(ctx context.Context, c *Certificate) error
	DeleteByDomain(ctx context.Context, domainID string) (int64, error)
	DeleteByProject(ctx context.Context, projectID string) (int64, error)
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

const cols = `id, org_id, project_id, domain_id, hostname, status, provider, resolver, issuer, serial, sans,
	issued_at, expires_at, renewed_at, last_probed_at, reissue_requested_at, renew_before_days, error, metadata, created_at, updated_at`

func scan(row pgx.Row, c *Certificate) error {
	return row.Scan(&c.ID, &c.OrgID, &c.ProjectID, &c.DomainID, &c.Hostname, &c.Status, &c.Provider, &c.Resolver,
		&c.Issuer, &c.Serial, &c.SANs, &c.IssuedAt, &c.ExpiresAt, &c.RenewedAt, &c.LastProbedAt, &c.ReissueRequestedAt,
		&c.RenewBeforeDays, &c.Error, &c.Metadata, &c.CreatedAt, &c.UpdatedAt)
}

func (s *PG) query(ctx context.Context, sql string, args ...any) ([]Certificate, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Certificate{}
	for rows.Next() {
		var c Certificate
		if err := scan(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *PG) Upsert(ctx context.Context, c *Certificate) (*Certificate, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	c.CreatedAt, c.UpdatedAt = now, now
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
	if len(c.SANs) == 0 {
		c.SANs = json.RawMessage(`[]`)
	}
	if c.DomainID != nil {
		if ex, err := s.byDomain(ctx, *c.DomainID); err == nil {
			// hostname may have changed (system domain moved): refresh it
			if ex.Hostname != c.Hostname {
				ex.Hostname = c.Hostname
				ex.Status = c.Status
				ex.IssuedAt, ex.ExpiresAt, ex.Issuer, ex.Error = nil, nil, "", ""
				_ = s.Update(ctx, ex)
			}
			return ex, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO certificates (id, org_id, project_id, domain_id, hostname, status, provider, resolver, issuer, serial, sans,
			issued_at, expires_at, renewed_at, last_probed_at, reissue_requested_at, renew_before_days, error, metadata, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		c.ID, c.OrgID, c.ProjectID, c.DomainID, c.Hostname, c.Status, c.Provider, c.Resolver, c.Issuer, c.Serial, c.SANs,
		c.IssuedAt, c.ExpiresAt, c.RenewedAt, c.LastProbedAt, c.ReissueRequestedAt, c.RenewBeforeDays, c.Error, c.Metadata,
		c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *PG) byDomain(ctx context.Context, domainID string) (*Certificate, error) {
	c := &Certificate{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM certificates WHERE domain_id=$1::uuid`, domainID), c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *PG) List(ctx context.Context, orgID, projectID string) ([]Certificate, error) {
	return s.query(ctx, `SELECT `+cols+` FROM certificates WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC`, orgID, projectID)
}

func (s *PG) ListByProject(ctx context.Context, projectID string) ([]Certificate, error) {
	return s.query(ctx, `SELECT `+cols+` FROM certificates WHERE project_id=$1::uuid ORDER BY created_at DESC`, projectID)
}

func (s *PG) ListAll(ctx context.Context) ([]Certificate, error) {
	return s.query(ctx, `SELECT `+cols+` FROM certificates ORDER BY created_at`)
}

func (s *PG) Get(ctx context.Context, orgID, projectID, id string) (*Certificate, error) {
	c := &Certificate{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM certificates WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID), c)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

func (s *PG) Update(ctx context.Context, c *Certificate) error {
	c.UpdatedAt = time.Now().UTC()
	if len(c.Metadata) == 0 {
		c.Metadata = json.RawMessage(`{}`)
	}
	if len(c.SANs) == 0 {
		c.SANs = json.RawMessage(`[]`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE certificates SET hostname=$2, status=$3, issuer=$4, serial=$5, sans=$6, issued_at=$7, expires_at=$8, renewed_at=$9,
			last_probed_at=$10, reissue_requested_at=$11, error=$12, metadata=$13, resolver=$14, updated_at=$15
		WHERE id=$1`,
		c.ID, c.Hostname, c.Status, c.Issuer, c.Serial, c.SANs, c.IssuedAt, c.ExpiresAt, c.RenewedAt,
		c.LastProbedAt, c.ReissueRequestedAt, c.Error, c.Metadata, c.Resolver, c.UpdatedAt)
	return err
}

func (s *PG) DeleteByDomain(ctx context.Context, domainID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM certificates WHERE domain_id=$1::uuid`, domainID)
	return tag.RowsAffected(), err
}

func (s *PG) DeleteByProject(ctx context.Context, projectID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM certificates WHERE project_id=$1::uuid`, projectID)
	return tag.RowsAffected(), err
}
