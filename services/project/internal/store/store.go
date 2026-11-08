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
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrDeleting      = errors.New("project is being deleted")
)

// Project statuses.
const (
	StatusActive   = "active"
	StatusDeleting = "deleting"
)

type Project struct {
	ID                string          `json:"id"`
	OrgID             string          `json:"org_id"`
	Name              string          `json:"name"`
	Slug              string          `json:"slug"`
	Description       string          `json:"description"`
	Status            string          `json:"status"`
	DeleteError       string          `json:"delete_error,omitempty"`
	JPConfig          json.RawMessage `json:"jp_config,omitempty"`
	JPConfigRaw       string          `json:"jp_config_raw,omitempty"`
	JPConfigHash      string          `json:"jp_config_hash,omitempty"`
	JPConfigAppliedAt *time.Time      `json:"jp_config_applied_at,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// Repo is the persistence surface used by the handlers (faked in tests).
type Repo interface {
	Create(ctx context.Context, orgID, name, slug, description string) (*Project, error)
	List(ctx context.Context, orgID string) ([]Project, error)
	Count(ctx context.Context, orgID string) (int, error)
	Get(ctx context.Context, orgID, projectID string) (*Project, error)
	Update(ctx context.Context, orgID, projectID string, name, description *string) (*Project, error)
	ApplyConfig(ctx context.Context, orgID, projectID, raw, hash string, cfg map[string]any) (*Project, error)
	// MarkDeleting flips the project to status "deleting" (idempotent).
	MarkDeleting(ctx context.Context, orgID, projectID string) (*Project, error)
	SetDeleteError(ctx context.Context, orgID, projectID, msg string) error
	Delete(ctx context.Context, orgID, projectID string) error
}

type PG struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

var _ Repo = (*PG)(nil)

const projectCols = `id, org_id, name, slug, description, status, delete_error, jp_config, jp_config_raw, jp_config_hash, jp_config_applied_at, created_at, updated_at`

func scanProject(row pgx.Row) (*Project, error) {
	p := &Project{}
	var cfg []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.Description, &p.Status, &p.DeleteError, &cfg, &p.JPConfigRaw, &p.JPConfigHash, &p.JPConfigAppliedAt, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(cfg) > 0 {
		p.JPConfig = json.RawMessage(cfg)
	}
	return p, nil
}

func (s *PG) Create(ctx context.Context, orgID, name, slug, description string) (*Project, error) {
	now := time.Now().UTC()
	p := &Project{ID: uuid.NewString(), OrgID: orgID, Name: name, Slug: slug, Description: description, Status: StatusActive, CreatedAt: now, UpdatedAt: now}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO projects (id, org_id, name, slug, description, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,'active',$6,$6)
	`, p.ID, p.OrgID, p.Name, p.Slug, p.Description, p.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return p, nil
}

func (s *PG) List(ctx context.Context, orgID string) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectCols+` FROM projects WHERE org_id=$1 ORDER BY created_at DESC LIMIT 1000`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p := Project{}
		var cfg []byte
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.Description, &p.Status, &p.DeleteError, &cfg, &p.JPConfigRaw, &p.JPConfigHash, &p.JPConfigAppliedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if len(cfg) > 0 {
			p.JPConfig = json.RawMessage(cfg)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PG) Count(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects WHERE org_id=$1 AND status='active'`, orgID).Scan(&n)
	return n, err
}

func (s *PG) Get(ctx context.Context, orgID, projectID string) (*Project, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := uuid.Parse(orgID); err != nil {
		return nil, ErrNotFound
	}
	return scanProject(s.pool.QueryRow(ctx, `SELECT `+projectCols+` FROM projects WHERE org_id=$1 AND id=$2`, orgID, projectID))
}

// Update changes name/description only: the slug is immutable because it determines the
// project's hostnames and network identity.
func (s *PG) Update(ctx context.Context, orgID, projectID string, name, description *string) (*Project, error) {
	p, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDeleting {
		return nil, ErrDeleting
	}
	if name != nil {
		p.Name = *name
	}
	if description != nil {
		p.Description = *description
	}
	p.UpdatedAt = time.Now().UTC()
	if _, err = s.pool.Exec(ctx, `UPDATE projects SET name=$1, description=$2, updated_at=$3 WHERE org_id=$4 AND id=$5`,
		p.Name, p.Description, p.UpdatedAt, orgID, projectID); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *PG) ApplyConfig(ctx context.Context, orgID, projectID, raw, hash string, cfg map[string]any) (*Project, error) {
	p, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	if p.Status == StatusDeleting {
		return nil, ErrDeleting
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if _, err = s.pool.Exec(ctx, `
		UPDATE projects SET jp_config=$1::jsonb, jp_config_raw=$2, jp_config_hash=$3, jp_config_applied_at=$4, updated_at=$4
		WHERE org_id=$5 AND id=$6
	`, string(b), raw, hash, now, orgID, projectID); err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, projectID)
}

func (s *PG) MarkDeleting(ctx context.Context, orgID, projectID string) (*Project, error) {
	if _, err := uuid.Parse(projectID); err != nil {
		return nil, ErrNotFound
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE projects SET status='deleting', deleting_at=COALESCE(deleting_at, NOW()), updated_at=NOW()
		WHERE org_id=$1 AND id=$2
	`, orgID, projectID); err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, projectID)
}

func (s *PG) SetDeleteError(ctx context.Context, orgID, projectID, msg string) error {
	if len(msg) > 4000 {
		msg = msg[:4000]
	}
	_, err := s.pool.Exec(ctx, `UPDATE projects SET delete_error=$3, updated_at=NOW() WHERE org_id=$1 AND id=$2`, orgID, projectID, msg)
	return err
}

func (s *PG) Delete(ctx context.Context, orgID, projectID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE org_id=$1 AND id=$2`, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint"))
}
