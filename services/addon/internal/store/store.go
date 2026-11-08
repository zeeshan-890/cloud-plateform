package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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

// Addon kinds.
const (
	KindCatalog   = "catalog"
	KindCustom    = "custom"
	KindManagedDB = "managed_db"
)

// Statuses (stored). Live status is merged in by the API.
const (
	StatusProvisioning = "provisioning"
	StatusReady        = "ready"
	StatusStopped      = "stopped"
	StatusFailed       = "failed"
	StatusDeleting     = "deleting"
)

type Addon struct {
	ID             string          `json:"id"`
	OrgID          string          `json:"org_id"`
	ProjectID      string          `json:"project_id"`
	Kind           string          `json:"kind"`
	Engine         string          `json:"engine"`
	Name           string          `json:"name"`
	Mode           string          `json:"mode"`
	Status         string          `json:"status"`
	Endpoint       string          `json:"endpoint"`
	Image          string          `json:"image"`
	ContainerID    string          `json:"container_id,omitempty"`
	VolumeName     string          `json:"volume_name,omitempty"`
	Metadata       json.RawMessage `json:"metadata"`
	SecretRef      string          `json:"secret_ref"`
	SecretKeys     []string        `json:"secret_keys"`
	Envs           []string        `json:"envs"`
	ConnectionHint string          `json:"connection_hint"`
	Error          string          `json:"error,omitempty"`
	CreatedBy      *string         `json:"created_by,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type Backup struct {
	ID                string     `json:"id"`
	AddonID           string     `json:"addon_id"`
	OrgID             string     `json:"org_id"`
	ProjectID         string     `json:"project_id"`
	Engine            string     `json:"engine"`
	Status            string     `json:"status"`
	ObjectKey         string     `json:"object_key"`
	SizeBytes         int64      `json:"size_bytes"`
	SHA256            string     `json:"sha256"`
	ObjectsCount      int64      `json:"objects_count"`
	Error             string     `json:"error,omitempty"`
	LastRestoreAt     *time.Time `json:"last_restore_at,omitempty"`
	LastRestoreStatus string     `json:"last_restore_status,omitempty"`
	LastRestoreError  string     `json:"last_restore_error,omitempty"`
	CreatedBy         *string    `json:"created_by,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
}

// Store is the persistence contract (implemented by *PG and test fakes).
type Store interface {
	Create(ctx context.Context, a *Addon) error
	Update(ctx context.Context, a *Addon) error
	List(ctx context.Context, orgID, projectID, engine string, includeManaged bool) ([]Addon, error)
	ListByProject(ctx context.Context, projectID string) ([]Addon, error)
	Get(ctx context.Context, orgID, projectID, id string) (*Addon, error)
	GetByID(ctx context.Context, id string) (*Addon, error)
	Delete(ctx context.Context, id string) error
	ListStaleProvisioning(ctx context.Context, olderThan time.Duration) ([]Addon, error)

	CreateBackup(ctx context.Context, b *Backup) error
	UpdateBackup(ctx context.Context, b *Backup) error
	ListBackups(ctx context.Context, addonID string) ([]Backup, error)
	GetBackup(ctx context.Context, addonID, id string) (*Backup, error)
	DeleteBackup(ctx context.Context, id string) error
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

const cols = `id, org_id, project_id, kind, engine, name, mode, status, endpoint, image, container_id, volume_name, metadata,
	secret_ref, secret_keys, envs, connection_hint, error, created_by, created_at, updated_at`

func scan(row pgx.Row, a *Addon) error {
	var keys, envs []byte
	if err := row.Scan(&a.ID, &a.OrgID, &a.ProjectID, &a.Kind, &a.Engine, &a.Name, &a.Mode, &a.Status, &a.Endpoint, &a.Image,
		&a.ContainerID, &a.VolumeName, &a.Metadata, &a.SecretRef, &keys, &envs, &a.ConnectionHint, &a.Error, &a.CreatedBy,
		&a.CreatedAt, &a.UpdatedAt); err != nil {
		return err
	}
	_ = json.Unmarshal(keys, &a.SecretKeys)
	_ = json.Unmarshal(envs, &a.Envs)
	if a.SecretKeys == nil {
		a.SecretKeys = []string{}
	}
	if a.Envs == nil {
		a.Envs = []string{}
	}
	return nil
}

func isUnique(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func (s *PG) Create(ctx context.Context, a *Addon) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	a.CreatedAt, a.UpdatedAt = now, now
	if a.Status == "" {
		a.Status = StatusProvisioning
	}
	if a.Kind == "" {
		a.Kind = KindCatalog
	}
	if a.Mode == "" {
		a.Mode = "container"
	}
	if len(a.Metadata) == 0 {
		a.Metadata = json.RawMessage(`{}`)
	}
	keys, _ := json.Marshal(nz(a.SecretKeys))
	envs, _ := json.Marshal(nz(a.Envs))
	_, err := s.pool.Exec(ctx, `
		INSERT INTO managed_addons (id, org_id, project_id, kind, engine, name, mode, status, endpoint, image, container_id, volume_name,
			metadata, secret_ref, secret_keys, envs, connection_hint, error, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)`,
		a.ID, a.OrgID, a.ProjectID, a.Kind, a.Engine, a.Name, a.Mode, a.Status, a.Endpoint, a.Image, a.ContainerID, a.VolumeName,
		a.Metadata, a.SecretRef, keys, envs, a.ConnectionHint, a.Error, a.CreatedBy, a.CreatedAt, a.UpdatedAt)
	if isUnique(err) {
		return ErrConflict
	}
	return err
}

func nz(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (s *PG) Update(ctx context.Context, a *Addon) error {
	a.UpdatedAt = time.Now().UTC()
	keys, _ := json.Marshal(nz(a.SecretKeys))
	envs, _ := json.Marshal(nz(a.Envs))
	if len(a.Metadata) == 0 {
		a.Metadata = json.RawMessage(`{}`)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE managed_addons SET status=$2, endpoint=$3, image=$4, container_id=$5, volume_name=$6, metadata=$7, secret_ref=$8,
			secret_keys=$9, envs=$10, connection_hint=$11, error=$12, updated_at=$13 WHERE id=$1`,
		a.ID, a.Status, a.Endpoint, a.Image, a.ContainerID, a.VolumeName, a.Metadata, a.SecretRef, keys, envs, a.ConnectionHint, a.Error, a.UpdatedAt)
	return err
}

func (s *PG) list(ctx context.Context, sql string, args ...any) ([]Addon, error) {
	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Addon{}
	for rows.Next() {
		var a Addon
		if err := scan(rows, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *PG) List(ctx context.Context, orgID, projectID, engine string, includeManaged bool) ([]Addon, error) {
	q := `SELECT ` + cols + ` FROM managed_addons WHERE org_id=$1`
	args := []any{orgID}
	if projectID != "" {
		args = append(args, projectID)
		q += ` AND project_id=$` + itoa(len(args))
	}
	if engine != "" {
		args = append(args, strings.ToLower(engine))
		q += ` AND engine=$` + itoa(len(args))
	}
	if !includeManaged {
		q += ` AND kind <> 'managed_db'`
	}
	return s.list(ctx, q+` ORDER BY created_at DESC`, args...)
}

func itoa(i int) string { return strconv.Itoa(i) }

func (s *PG) ListByProject(ctx context.Context, projectID string) ([]Addon, error) {
	return s.list(ctx, `SELECT `+cols+` FROM managed_addons WHERE project_id=$1::uuid`, projectID)
}

func (s *PG) ListStaleProvisioning(ctx context.Context, olderThan time.Duration) ([]Addon, error) {
	return s.list(ctx, `SELECT `+cols+` FROM managed_addons WHERE status='provisioning' AND updated_at < $1`, time.Now().UTC().Add(-olderThan))
}

func (s *PG) Get(ctx context.Context, orgID, projectID, id string) (*Addon, error) {
	a := &Addon{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM managed_addons WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID), a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *PG) GetByID(ctx context.Context, id string) (*Addon, error) {
	a := &Addon{}
	err := scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM managed_addons WHERE id=$1`, id), a)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return a, err
}

func (s *PG) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM managed_addons WHERE id=$1`, id)
	return err
}

const bcols = `id, addon_id, org_id, project_id, engine, status, object_key, size_bytes, sha256, objects_count, error,
	last_restore_at, last_restore_status, last_restore_error, created_by, created_at, completed_at`

func scanBackup(row pgx.Row, b *Backup) error {
	return row.Scan(&b.ID, &b.AddonID, &b.OrgID, &b.ProjectID, &b.Engine, &b.Status, &b.ObjectKey, &b.SizeBytes, &b.SHA256,
		&b.ObjectsCount, &b.Error, &b.LastRestoreAt, &b.LastRestoreStatus, &b.LastRestoreError, &b.CreatedBy, &b.CreatedAt, &b.CompletedAt)
}

func (s *PG) CreateBackup(ctx context.Context, b *Backup) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	b.CreatedAt = time.Now().UTC()
	if b.Status == "" {
		b.Status = "running"
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO addon_backups (id, addon_id, org_id, project_id, engine, status, object_key, objects_count, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, b.ID, b.AddonID, b.OrgID, b.ProjectID, b.Engine, b.Status, b.ObjectKey, -1, b.CreatedBy, b.CreatedAt)
	return err
}

func (s *PG) UpdateBackup(ctx context.Context, b *Backup) error {
	_, err := s.pool.Exec(ctx, `UPDATE addon_backups SET status=$2, size_bytes=$3, sha256=$4, objects_count=$5, error=$6,
		last_restore_at=$7, last_restore_status=$8, last_restore_error=$9, completed_at=$10 WHERE id=$1`,
		b.ID, b.Status, b.SizeBytes, b.SHA256, b.ObjectsCount, b.Error, b.LastRestoreAt, b.LastRestoreStatus, b.LastRestoreError, b.CompletedAt)
	return err
}

func (s *PG) ListBackups(ctx context.Context, addonID string) ([]Backup, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+bcols+` FROM addon_backups WHERE addon_id=$1 ORDER BY created_at DESC LIMIT 200`, addonID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		var b Backup
		if err := scanBackup(rows, &b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PG) GetBackup(ctx context.Context, addonID, id string) (*Backup, error) {
	b := &Backup{}
	err := scanBackup(s.pool.QueryRow(ctx, `SELECT `+bcols+` FROM addon_backups WHERE id=$1 AND addon_id=$2`, id, addonID), b)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *PG) DeleteBackup(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM addon_backups WHERE id=$1`, id)
	return err
}
