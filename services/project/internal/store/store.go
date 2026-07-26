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

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

type Project struct {
	ID                 string          `json:"id"`
	OrgID              string          `json:"org_id"`
	Name               string          `json:"name"`
	Slug               string          `json:"slug"`
	Description        string          `json:"description"`
	JPConfig           json.RawMessage `json:"jp_config,omitempty"`
	JPConfigRaw        string          `json:"jp_config_raw,omitempty"`
	JPConfigHash       string          `json:"jp_config_hash,omitempty"`
	JPConfigAppliedAt  *time.Time      `json:"jp_config_applied_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const projectCols = `id, org_id, name, slug, description, jp_config, jp_config_raw, jp_config_hash, jp_config_applied_at, created_at, updated_at`

func scanProject(row pgx.Row) (*Project, error) {
	p := &Project{}
	var cfg []byte
	err := row.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.Description, &cfg, &p.JPConfigRaw, &p.JPConfigHash, &p.JPConfigAppliedAt, &p.CreatedAt, &p.UpdatedAt)
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

func (s *Store) Create(ctx context.Context, orgID, name, slug, description string) (*Project, error) {
	now := time.Now().UTC()
	p := &Project{
		ID: uuid.NewString(), OrgID: orgID, Name: name, Slug: slug,
		Description: description, CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO projects (id, org_id, name, slug, description, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$6)
	`, p.ID, p.OrgID, p.Name, p.Slug, p.Description, p.CreatedAt)
	if err != nil {
		if contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint") {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return p, nil
}

func (s *Store) List(ctx context.Context, orgID string) ([]Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+projectCols+` FROM projects WHERE org_id=$1 ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Project
	for rows.Next() {
		p := Project{}
		var cfg []byte
		if err := rows.Scan(&p.ID, &p.OrgID, &p.Name, &p.Slug, &p.Description, &cfg, &p.JPConfigRaw, &p.JPConfigHash, &p.JPConfigAppliedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if len(cfg) > 0 {
			p.JPConfig = json.RawMessage(cfg)
		}
		out = append(out, p)
	}
	if out == nil {
		out = []Project{}
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, projectID string) (*Project, error) {
	return scanProject(s.pool.QueryRow(ctx, `
		SELECT `+projectCols+` FROM projects WHERE org_id=$1 AND id=$2
	`, orgID, projectID))
}

func (s *Store) Update(ctx context.Context, orgID, projectID string, name, slug, description *string) (*Project, error) {
	p, err := s.Get(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	if name != nil {
		p.Name = *name
	}
	if slug != nil {
		p.Slug = *slug
	}
	if description != nil {
		p.Description = *description
	}
	p.UpdatedAt = time.Now().UTC()
	_, err = s.pool.Exec(ctx, `
		UPDATE projects SET name=$1, slug=$2, description=$3, updated_at=$4
		WHERE org_id=$5 AND id=$6
	`, p.Name, p.Slug, p.Description, p.UpdatedAt, orgID, projectID)
	if err != nil {
		if contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint") {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return p, nil
}

func (s *Store) ApplyConfig(ctx context.Context, orgID, projectID, raw, hash string, cfg map[string]any) (*Project, error) {
	if _, err := s.Get(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	_, err = s.pool.Exec(ctx, `
		UPDATE projects SET jp_config=$1::jsonb, jp_config_raw=$2, jp_config_hash=$3,
			jp_config_applied_at=$4, updated_at=$4
		WHERE org_id=$5 AND id=$6
	`, string(b), raw, hash, now, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, orgID, projectID)
}

func (s *Store) Delete(ctx context.Context, orgID, projectID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM projects WHERE org_id=$1 AND id=$2`, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
