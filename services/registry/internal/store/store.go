package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Image struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	ImageRef  string    `json:"image_ref"`
	Framework string    `json:"framework"`
	GitSHA    string    `json:"git_sha"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) Create(ctx context.Context, img *Image) error {
	if img.ID == "" {
		img.ID = uuid.NewString()
	}
	img.CreatedAt = time.Now().UTC()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO images (id, org_id, project_id, image_ref, framework, git_sha, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
	`, img.ID, img.OrgID, img.ProjectID, img.ImageRef, img.Framework, img.GitSHA, img.CreatedAt)
	return err
}

func (s *Store) List(ctx context.Context, orgID, projectID string) ([]Image, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, image_ref, framework, git_sha, created_at
		FROM images WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT 100
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var i Image
		if err := rows.Scan(&i.ID, &i.OrgID, &i.ProjectID, &i.ImageRef, &i.Framework, &i.GitSHA, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if out == nil {
		out = []Image{}
	}
	return out, rows.Err()
}

// Candidates returns images that may be removed: older than olderThan, not among the
// newest keepPerProject images of their project and not in keepRefs.
func (s *Store) Candidates(ctx context.Context, olderThan time.Time, keepPerProject int, keepRefs map[string]struct{}) ([]Image, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, image_ref, framework, git_sha, created_at FROM (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY project_id ORDER BY created_at DESC) AS rn FROM images
		) t WHERE rn > $1 AND created_at < $2 ORDER BY created_at LIMIT 500
	`, keepPerProject, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var i Image
		if err := rows.Scan(&i.ID, &i.OrgID, &i.ProjectID, &i.ImageRef, &i.Framework, &i.GitSHA, &i.CreatedAt); err != nil {
			return nil, err
		}
		if _, keep := keepRefs[i.ImageRef]; !keep {
			out = append(out, i)
		}
	}
	return out, rows.Err()
}

// ListAllByProject returns every image of a project.
func (s *Store) ListAllByProject(ctx context.Context, projectID string) ([]Image, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, project_id, image_ref, framework, git_sha, created_at FROM images WHERE project_id=$1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var i Image
		if err := rows.Scan(&i.ID, &i.OrgID, &i.ProjectID, &i.ImageRef, &i.Framework, &i.GitSHA, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// Delete removes one metadata row.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM images WHERE id=$1`, id)
	return err
}

// DeleteByRef removes the metadata of one image ref of a project.
func (s *Store) DeleteByRef(ctx context.Context, projectID, ref string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM images WHERE project_id=$1 AND image_ref=$2`, projectID, ref)
	return err
}
