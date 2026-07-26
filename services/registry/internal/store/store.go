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

// DeleteOrphans removes image metadata older than cutoff, keeping the newest
// image per project and any refs in keepRefs. Returns deleted count.
func (s *Store) DeleteOrphans(ctx context.Context, olderThan time.Time, keepRefs map[string]struct{}) (int, error) {
	if keepRefs == nil {
		keepRefs = map[string]struct{}{}
	}
	// Protect latest image per project
	rowsKeep, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (org_id, project_id) image_ref
		FROM images ORDER BY org_id, project_id, created_at DESC
	`)
	if err != nil {
		return 0, err
	}
	defer rowsKeep.Close()
	for rowsKeep.Next() {
		var ref string
		if err := rowsKeep.Scan(&ref); err != nil {
			return 0, err
		}
		keepRefs[ref] = struct{}{}
	}
	if err := rowsKeep.Err(); err != nil {
		return 0, err
	}

	rows, err := s.pool.Query(ctx, `SELECT id, image_ref FROM images WHERE created_at < $1`, olderThan)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id, ref string
		if err := rows.Scan(&id, &ref); err != nil {
			return 0, err
		}
		if _, ok := keepRefs[ref]; ok {
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		tag, err := s.pool.Exec(ctx, `DELETE FROM images WHERE id=$1`, id)
		if err != nil {
			continue
		}
		n += int(tag.RowsAffected())
	}
	return n, nil
}
