package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

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

// Store is the persistence contract (implemented by *PG and test fakes).
type Store interface {
	EnsureBucket(ctx context.Context, orgID, projectID string) (*Bucket, error)
	GetBucket(ctx context.Context, projectID string) (*Bucket, error)
	DeleteBucket(ctx context.Context, projectID string) error
	// UpsertObject records an object and returns the size of the version it replaced (0 if new).
	UpsertObject(ctx context.Context, b *Bucket, key, contentType, etag string, size int64) (obj *Object, prevSize int64, err error)
	GetObject(ctx context.Context, projectID, key string) (*Object, error)
	ListObjects(ctx context.Context, projectID, prefix, after string, limit int) ([]Object, error)
	DeleteObject(ctx context.Context, projectID, key string) error
	DeleteByPrefix(ctx context.Context, projectID, prefix string) ([]string, error)
	Usage(ctx context.Context, projectID string) (bytes, count int64, err error)
}

// BucketName derives the MinIO bucket from the PROJECT id only: jp-<first 8 hex>-<6 hex of sha256(project id)>.
func BucketName(projectID string) string {
	id := strings.ToLower(strings.ReplaceAll(projectID, "-", ""))
	short := id
	if len(short) > 8 {
		short = short[:8]
	}
	sum := sha256.Sum256([]byte(strings.ToLower(projectID)))
	return "jp-" + short + "-" + hex.EncodeToString(sum[:])[:6]
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

func (s *PG) GetBucket(ctx context.Context, projectID string) (*Bucket, error) {
	b := &Bucket{}
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, project_id, name, mode, created_at FROM storage_buckets WHERE project_id=$1::uuid`, projectID).
		Scan(&b.ID, &b.OrgID, &b.ProjectID, &b.Name, &b.Mode, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return b, err
}

func (s *PG) EnsureBucket(ctx context.Context, orgID, projectID string) (*Bucket, error) {
	if b, err := s.GetBucket(ctx, projectID); err == nil {
		return b, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	b := &Bucket{ID: uuid.NewString(), OrgID: orgID, ProjectID: projectID, Name: BucketName(projectID), Mode: "minio", CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `INSERT INTO storage_buckets (id, org_id, project_id, name, mode, created_at) VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (project_id) DO NOTHING`, b.ID, b.OrgID, b.ProjectID, b.Name, b.Mode, b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return s.GetBucket(ctx, projectID)
}

func (s *PG) DeleteBucket(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM storage_buckets WHERE project_id=$1::uuid`, projectID)
	return err
}

const ocols = `id, bucket_id, org_id, project_id, object_key, size_bytes, content_type, etag, created_at, updated_at`

func scanObj(row pgx.Row, o *Object) error {
	return row.Scan(&o.ID, &o.BucketID, &o.OrgID, &o.ProjectID, &o.Key, &o.SizeBytes, &o.ContentType, &o.ETag, &o.CreatedAt, &o.UpdatedAt)
}

func (s *PG) UpsertObject(ctx context.Context, b *Bucket, key, contentType, etag string, size int64) (*Object, int64, error) {
	var prev int64
	_ = s.pool.QueryRow(ctx, `SELECT size_bytes FROM storage_objects WHERE bucket_id=$1 AND object_key=$2`, b.ID, key).Scan(&prev)
	now := time.Now().UTC()
	o := &Object{}
	err := scanObj(s.pool.QueryRow(ctx, `
		INSERT INTO storage_objects (id, bucket_id, org_id, project_id, object_key, size_bytes, content_type, etag, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)
		ON CONFLICT (bucket_id, object_key) DO UPDATE SET size_bytes=EXCLUDED.size_bytes, content_type=EXCLUDED.content_type,
			etag=EXCLUDED.etag, updated_at=EXCLUDED.updated_at
		RETURNING `+ocols, uuid.NewString(), b.ID, b.OrgID, b.ProjectID, key, size, contentType, etag, now), o)
	return o, prev, err
}

func (s *PG) GetObject(ctx context.Context, projectID, key string) (*Object, error) {
	o := &Object{}
	err := scanObj(s.pool.QueryRow(ctx, `SELECT `+ocols+` FROM storage_objects WHERE project_id=$1::uuid AND object_key=$2`, projectID, key), o)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *PG) ListObjects(ctx context.Context, projectID, prefix, after string, limit int) ([]Object, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ocols+` FROM storage_objects WHERE project_id=$1::uuid AND object_key LIKE $2 ESCAPE '\' AND object_key > $3
		ORDER BY object_key LIMIT $4`, projectID, likeEscape(prefix)+"%", after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		var o Object
		if err := scanObj(rows, &o); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *PG) DeleteObject(ctx context.Context, projectID, key string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM storage_objects WHERE project_id=$1::uuid AND object_key=$2`, projectID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) DeleteByPrefix(ctx context.Context, projectID, prefix string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `DELETE FROM storage_objects WHERE project_id=$1::uuid AND object_key LIKE $2 ESCAPE '\' RETURNING object_key`,
		projectID, likeEscape(prefix)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *PG) Usage(ctx context.Context, projectID string) (int64, int64, error) {
	var b, c int64
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(SUM(size_bytes),0), COUNT(*) FROM storage_objects WHERE project_id=$1::uuid`, projectID).Scan(&b, &c)
	return b, c, err
}
