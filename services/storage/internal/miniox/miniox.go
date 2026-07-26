package miniox

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Backend abstracts object storage (MinIO or in-memory simulate).
type Backend interface {
	Mode() string
	EnsureBucket(ctx context.Context, name string) error
	Put(ctx context.Context, bucket, key, contentType string, body []byte) (etag string, size int64, err error)
	List(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error)
	Delete(ctx context.Context, bucket, key string) error
	SignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error)
}

type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

type simulateBackend struct {
	objects map[string]map[string]simObj
}

type simObj struct {
	Data        []byte
	ContentType string
	ETag        string
	UpdatedAt   time.Time
}

func NewFromEnv() (Backend, error) {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("STORAGE_MODE")))
	if mode == "" {
		mode = "simulate"
	}
	if mode == "simulate" {
		return &simulateBackend{objects: map[string]map[string]simObj{}}, nil
	}

	endpoint := os.Getenv("MINIO_ENDPOINT")
	if endpoint == "" {
		endpoint = "localhost:9000"
	}
	access := os.Getenv("MINIO_ACCESS_KEY")
	if access == "" {
		access = "minioadmin"
	}
	secret := os.Getenv("MINIO_SECRET_KEY")
	if secret == "" {
		secret = "minioadmin"
	}
	useSSL := strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	// Probe; fall back to simulate if MinIO unreachable
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = client.ListBuckets(ctx)
	if err != nil {
		return &simulateBackend{objects: map[string]map[string]simObj{}}, nil
	}
	return &minioBackend{client: client, publicBase: os.Getenv("MINIO_PUBLIC_URL")}, nil
}

func (s *simulateBackend) Mode() string { return "simulate" }

func (s *simulateBackend) EnsureBucket(_ context.Context, name string) error {
	if s.objects[name] == nil {
		s.objects[name] = map[string]simObj{}
	}
	return nil
}

func (s *simulateBackend) Put(_ context.Context, bucket, key, contentType string, body []byte) (string, int64, error) {
	if s.objects[bucket] == nil {
		s.objects[bucket] = map[string]simObj{}
	}
	etag := fmt.Sprintf("sim-%d", len(body))
	s.objects[bucket][key] = simObj{Data: append([]byte(nil), body...), ContentType: contentType, ETag: etag, UpdatedAt: time.Now().UTC()}
	return etag, int64(len(body)), nil
}

func (s *simulateBackend) List(_ context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	m := s.objects[bucket]
	out := []ObjectInfo{}
	for k, v := range m {
		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}
		out = append(out, ObjectInfo{Key: k, Size: int64(len(v.Data)), ContentType: v.ContentType, ETag: v.ETag, LastModified: v.UpdatedAt})
	}
	return out, nil
}

func (s *simulateBackend) Delete(_ context.Context, bucket, key string) error {
	if m := s.objects[bucket]; m != nil {
		delete(m, key)
	}
	return nil
}

func (s *simulateBackend) SignedURL(_ context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u := url.URL{
		Scheme:   "http",
		Host:     "localhost:9000",
		Path:     fmt.Sprintf("/%s/%s", bucket, key),
		RawQuery: fmt.Sprintf("simulate=1&expires=%d", int(expiry.Seconds())),
	}
	return u.String(), nil
}

type minioBackend struct {
	client     *minio.Client
	publicBase string
}

func (m *minioBackend) Mode() string { return "minio" }

func (m *minioBackend) EnsureBucket(ctx context.Context, name string) error {
	exists, err := m.client.BucketExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return m.client.MakeBucket(ctx, name, minio.MakeBucketOptions{})
}

func (m *minioBackend) Put(ctx context.Context, bucket, key, contentType string, body []byte) (string, int64, error) {
	info, err := m.client.PutObject(ctx, bucket, key, bytes.NewReader(body), int64(len(body)), minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", 0, err
	}
	return info.ETag, info.Size, nil
}

func (m *minioBackend) List(ctx context.Context, bucket, prefix string) ([]ObjectInfo, error) {
	out := []ObjectInfo{}
	for obj := range m.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		out = append(out, ObjectInfo{Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType, ETag: obj.ETag, LastModified: obj.LastModified})
	}
	return out, nil
}

func (m *minioBackend) Delete(ctx context.Context, bucket, key string) error {
	return m.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (m *minioBackend) SignedURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	u, err := m.client.PresignedGetObject(ctx, bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	if m.publicBase != "" {
		base, err := url.Parse(m.publicBase)
		if err == nil {
			u.Scheme = base.Scheme
			u.Host = base.Host
		}
	}
	return u.String(), nil
}
