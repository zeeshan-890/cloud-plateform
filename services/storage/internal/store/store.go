package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	bucketSafe  = regexp.MustCompile(`[^a-z0-9-]`)
)

type Bucket struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"created_at"`
}

type Object struct {
	ID          string    `json:"id"`
	BucketID    string    `json:"bucket_id"`
	OrgID       string    `json:"org_id"`
	ProjectID   string    `json:"project_id"`
	Key         string    `json:"key"`
	SizeBytes   int64     `json:"size_bytes"`
	ContentType string    `json:"content_type"`
	ETag        string    `json:"etag"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func BucketName(orgID, projectID string) string {
	raw := fmt.Sprintf("jp-%s-%s", shortID(orgID), shortID(projectID))
	raw = strings.ToLower(raw)
	raw = bucketSafe.ReplaceAllString(raw, "")
	if len(raw) > 63 {
		raw = raw[:63]
	}
	if raw == "" {
		raw = "jp-bucket"
	}
	return raw
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func (s *Store) EnsureBucket(ctx context.Context, orgID, projectID, mode string) (*Bucket, error) {
	b, err := s.GetBucket(ctx, orgID, projectID)
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	now := time.Now().UTC()
	b = &Bucket{
		ID: uuid.NewString(), OrgID: orgID, ProjectID: projectID,
		Name: BucketName(orgID, projectID), Mode: mode, CreatedAt: now,
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO storage_buckets (id, org_id, project_id, name, mode, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, project_id) DO NOTHING
	`, b.ID, b.OrgID, b.ProjectID, b.Name, b.Mode, b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetBucket(ctx, orgID, projectID)
}

func (s *Store) GetBucket(ctx context.Context, orgID, projectID string) (*Bucket, error) {
	b := &Bucket{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, name, mode, created_at
		FROM storage_buckets WHERE org_id=$1 AND project_id=$2
	`, orgID, projectID).Scan(&b.ID, &b.OrgID, &b.ProjectID, &b.Name, &b.Mode, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *Store) UpsertObject(ctx context.Context, orgID, projectID, bucketID, key, contentType, etag string, size int64) (*Object, error) {
	now := time.Now().UTC()
	o := &Object{
		ID: uuid.NewString(), BucketID: bucketID, OrgID: orgID, ProjectID: projectID,
		Key: key, SizeBytes: size, ContentType: contentType, ETag: etag,
		CreatedAt: now, UpdatedAt: now,
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO storage_objects (id, bucket_id, org_id, project_id, object_key, size_bytes, content_type, etag, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (bucket_id, object_key) DO UPDATE SET
			size_bytes=EXCLUDED.size_bytes, content_type=EXCLUDED.content_type, etag=EXCLUDED.etag, updated_at=EXCLUDED.updated_at
	`, o.ID, o.BucketID, o.OrgID, o.ProjectID, o.Key, o.SizeBytes, o.ContentType, o.ETag, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetObject(ctx, orgID, projectID, key)
}

func (s *Store) GetObject(ctx context.Context, orgID, projectID, key string) (*Object, error) {
	o := &Object{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, bucket_id, org_id, project_id, object_key, size_bytes, content_type, etag, created_at, updated_at
		FROM storage_objects WHERE org_id=$1 AND project_id=$2 AND object_key=$3
	`, orgID, projectID, key).Scan(&o.ID, &o.BucketID, &o.OrgID, &o.ProjectID, &o.Key, &o.SizeBytes, &o.ContentType, &o.ETag, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) ListObjects(ctx context.Context, orgID, projectID, prefix string) ([]Object, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, bucket_id, org_id, project_id, object_key, size_bytes, content_type, etag, created_at, updated_at
		FROM storage_objects WHERE org_id=$1 AND project_id=$2
		  AND ($3 = '' OR object_key LIKE $3 || '%')
		ORDER BY object_key ASC LIMIT 500
	`, orgID, projectID, prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		var o Object
		if err := rows.Scan(&o.ID, &o.BucketID, &o.OrgID, &o.ProjectID, &o.Key, &o.SizeBytes, &o.ContentType, &o.ETag, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) DeleteObject(ctx context.Context, orgID, projectID, key string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM storage_objects WHERE org_id=$1 AND project_id=$2 AND object_key=$3`, orgID, projectID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
