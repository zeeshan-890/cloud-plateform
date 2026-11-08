package store

import (
	"context"
	"encoding/json"
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

// Domain kinds.
const (
	KindCustom  = "custom"
	KindSystem  = "system"
	KindPreview = "preview"
)

// Domain statuses.
const (
	StatusPending  = "pending"  // not verified: no router, no certificate
	StatusVerified = "verified" // DNS proven, router written
	StatusActive   = "active"   // verified and a certificate exists (or system/preview)
	StatusFailed   = "failed"   // was verified, DNS no longer validates: router removed
)

// Routed reports whether a status gets a router.
func Routed(status string) bool { return status == StatusVerified || status == StatusActive }

type Domain struct {
	ID                string     `json:"id"`
	OrgID             string     `json:"org_id"`
	ProjectID         string     `json:"project_id"`
	DeploymentID      *string    `json:"deployment_id,omitempty"`
	Hostname          string     `json:"hostname"`
	Wildcard          bool       `json:"wildcard,omitempty"`
	Kind              string     `json:"kind"`
	Status            string     `json:"status"`
	VerificationType  string     `json:"verification_type"`
	VerificationToken string     `json:"-"` // exposed only through the verification instructions
	VerifiedMethod    string     `json:"verified_method,omitempty"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	Service           *string    `json:"-"`
	TraefikFile       string     `json:"-"`
	CertificateID     *string    `json:"certificate_id,omitempty"`
	DNSLastChecked    *time.Time `json:"dns_last_checked,omitempty"`
	DNSError          string     `json:"dns_error,omitempty"`
	DNSFailCount      int        `json:"-"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Store is the persistence contract used by handlers (implemented by *PG and by test fakes).
type Store interface {
	Create(ctx context.Context, d *Domain) error
	List(ctx context.Context, orgID, projectID string) ([]Domain, error)
	Get(ctx context.Context, orgID, projectID, id string) (*Domain, error)
	GetByID(ctx context.Context, id string) (*Domain, error)
	Update(ctx context.Context, d *Domain) error
	Delete(ctx context.Context, orgID, projectID, id string) error
	DeleteByID(ctx context.Context, id string) error
	FindByDeploymentID(ctx context.Context, deploymentID string) ([]Domain, error)
	ListByProject(ctx context.Context, projectID string) ([]Domain, error)
	ListRouted(ctx context.Context) ([]Domain, error)
	ListReverify(ctx context.Context) ([]Domain, error)
	DeleteByProject(ctx context.Context, projectID string) ([]Domain, error)
	FindSystemByProject(ctx context.Context, projectID string) (*Domain, error)

	GetRoute(ctx context.Context, projectID string) (string, error)
	SetRoute(ctx context.Context, projectID, service string, upstreams []string) error
	ListRoutes(ctx context.Context) (map[string]string, error)
	// ListUpstreams returns stored upstreams per project (projects without upstreams are absent).
	ListUpstreams(ctx context.Context) (map[string][]string, error)
	DeleteRoute(ctx context.Context, projectID string) error
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

const cols = `id, org_id, project_id, deployment_id, hostname, wildcard, kind, status, verification_type,
	verification_token, verified_method, verified_at, service, traefik_file, certificate_id,
	dns_last_checked, dns_error, dns_fail_count, created_at, updated_at`

func scan(row pgx.Row, d *Domain) error {
	return row.Scan(&d.ID, &d.OrgID, &d.ProjectID, &d.DeploymentID, &d.Hostname, &d.Wildcard, &d.Kind, &d.Status,
		&d.VerificationType, &d.VerificationToken, &d.VerifiedMethod, &d.VerifiedAt, &d.Service, &d.TraefikFile,
		&d.CertificateID, &d.DNSLastChecked, &d.DNSError, &d.DNSFailCount, &d.CreatedAt, &d.UpdatedAt)
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (s *PG) query(ctx context.Context, sql string, args ...any) ([]Domain, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := scan(rows, &d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *PG) Create(ctx context.Context, d *Domain) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	d.CreatedAt, d.UpdatedAt = now, now
	if d.Status == "" {
		d.Status = StatusPending
	}
	if d.Kind == "" {
		d.Kind = KindCustom
	}
	if d.VerificationType == "" {
		d.VerificationType = "txt"
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO domains (id, org_id, project_id, deployment_id, hostname, wildcard, kind, status, verification_type,
			verification_token, verified_method, verified_at, service, traefik_file, certificate_id,
			dns_last_checked, dns_error, dns_fail_count, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		d.ID, d.OrgID, d.ProjectID, d.DeploymentID, d.Hostname, d.Wildcard, d.Kind, d.Status, d.VerificationType,
		d.VerificationToken, d.VerifiedMethod, d.VerifiedAt, d.Service, d.TraefikFile, d.CertificateID,
		d.DNSLastChecked, d.DNSError, d.DNSFailCount, d.CreatedAt, d.UpdatedAt)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (s *PG) List(ctx context.Context, orgID, projectID string) ([]Domain, error) {
	return s.query(ctx, `SELECT `+cols+` FROM domains WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC`, orgID, projectID)
}

func (s *PG) Get(ctx context.Context, orgID, projectID, id string) (*Domain, error) {
	d := &Domain{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM domains WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID), d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *PG) GetByID(ctx context.Context, id string) (*Domain, error) {
	d := &Domain{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM domains WHERE id=$1`, id), d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// Update persists mutable fields. Promoting to verified/active can hit the routed-host
// unique index (another project already holds the hostname) -> ErrConflict.
func (s *PG) Update(ctx context.Context, d *Domain) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		UPDATE domains SET status=$2, verified_method=$3, verified_at=$4, service=$5, traefik_file=$6,
			certificate_id=$7, deployment_id=$8, dns_last_checked=$9, dns_error=$10, dns_fail_count=$11,
			verification_type=$12, updated_at=$13
		WHERE id=$1`,
		d.ID, d.Status, d.VerifiedMethod, d.VerifiedAt, d.Service, d.TraefikFile, d.CertificateID, d.DeploymentID,
		d.DNSLastChecked, d.DNSError, d.DNSFailCount, d.VerificationType, d.UpdatedAt)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func (s *PG) Delete(ctx context.Context, orgID, projectID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) DeleteByID(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) FindByDeploymentID(ctx context.Context, deploymentID string) ([]Domain, error) {
	return s.query(ctx, `SELECT `+cols+` FROM domains WHERE deployment_id=$1::uuid`, deploymentID)
}

func (s *PG) ListByProject(ctx context.Context, projectID string) ([]Domain, error) {
	return s.query(ctx, `SELECT `+cols+` FROM domains WHERE project_id=$1::uuid ORDER BY created_at`, projectID)
}

func (s *PG) ListRouted(ctx context.Context) ([]Domain, error) {
	return s.query(ctx, `SELECT `+cols+` FROM domains WHERE status IN ('verified','active')`)
}

func (s *PG) ListReverify(ctx context.Context) ([]Domain, error) {
	return s.query(ctx, `SELECT `+cols+` FROM domains WHERE kind='custom' AND status IN ('verified','active')`)
}

func (s *PG) DeleteByProject(ctx context.Context, projectID string) ([]Domain, error) {
	list, err := s.ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM domains WHERE project_id=$1::uuid`, projectID); err != nil {
		return nil, err
	}
	_ = s.DeleteRoute(ctx, projectID)
	return list, nil
}

func (s *PG) FindSystemByProject(ctx context.Context, projectID string) (*Domain, error) {
	d := &Domain{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM domains WHERE project_id=$1::uuid AND kind='system' LIMIT 1`, projectID), d)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

func (s *PG) GetRoute(ctx context.Context, projectID string) (string, error) {
	var svc string
	err := s.pool.QueryRow(ctx, `SELECT service FROM project_routes WHERE project_id=$1::uuid`, projectID).Scan(&svc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return svc, err
}

func (s *PG) SetRoute(ctx context.Context, projectID, service string, upstreams []string) error {
	if upstreams == nil {
		upstreams = []string{}
	}
	js, err := json.Marshal(upstreams)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO project_routes (project_id, service, upstreams, updated_at) VALUES ($1::uuid,$2,$3,NOW())
		ON CONFLICT (project_id) DO UPDATE SET service=EXCLUDED.service, upstreams=EXCLUDED.upstreams, updated_at=NOW()`,
		projectID, service, string(js))
	return err
}

func (s *PG) ListUpstreams(ctx context.Context) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT project_id::text, upstreams FROM project_routes WHERE upstreams <> '[]'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var p, js string
		if err := rows.Scan(&p, &js); err != nil {
			return nil, err
		}
		var l []string
		if json.Unmarshal([]byte(js), &l) == nil && len(l) > 0 {
			out[p] = l
		}
	}
	return out, rows.Err()
}

func (s *PG) ListRoutes(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT project_id::text, service FROM project_routes`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, svc string
		if err := rows.Scan(&p, &svc); err != nil {
			return nil, err
		}
		out[p] = svc
	}
	return out, rows.Err()
}

func (s *PG) DeleteRoute(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM project_routes WHERE project_id=$1::uuid`, projectID)
	return err
}
