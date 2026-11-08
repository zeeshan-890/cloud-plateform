// Package miniox is the object backend. Production uses MinIO only; there is no simulate mode
// reachable from the environment (Memory exists for unit tests and must be wired explicitly).
package miniox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound is returned when an object does not exist.
var ErrNotFound = errors.New("object not found")

type ObjectInfo struct {
	Key          string
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

// Backend abstracts object storage.
type Backend interface {
	Name() string
	EnsureBucket(ctx context.Context, name string) error
	// Put streams r into the object; size < 0 means unknown. It returns the stored size.
	Put(ctx context.Context, bucket, key, contentType string, r io.Reader, size int64) (etag string, stored int64, err error)
	Get(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error)
	Delete(ctx context.Context, bucket, key string) error
	// DeleteBucket removes every object and then the bucket.
	DeleteBucket(ctx context.Context, bucket string) error
	// SignedURL presigns a GET. publicBase (scheme://host) replaces the host when set: the signature
	// only validates if MinIO is really reachable on that host.
	SignedURL(ctx context.Context, bucket, key string, expiry time.Duration, publicBase string) (string, error)
}

// Config for the MinIO backend.
type Config struct {
	Endpoint, AccessKey, SecretKey string
	UseSSL                         bool
}

// ConfigFromEnv reads MINIO_*; credentials are mandatory and must not be the well known defaults in production.
func ConfigFromEnv() (Config, error) {
	c := Config{
		Endpoint:  os.Getenv("MINIO_ENDPOINT"),
		AccessKey: os.Getenv("MINIO_ACCESS_KEY"),
		SecretKey: os.Getenv("MINIO_SECRET_KEY"),
		UseSSL:    strings.EqualFold(os.Getenv("MINIO_USE_SSL"), "true"),
	}
	if c.Endpoint == "" {
		return c, errors.New("MINIO_ENDPOINT is required")
	}
	if c.AccessKey == "" || c.SecretKey == "" {
		return c, errors.New("MINIO_ACCESS_KEY and MINIO_SECRET_KEY are required (no default credentials)")
	}
	if strings.EqualFold(os.Getenv("JP_ENV"), "production") {
		if c.AccessKey == "minioadmin" || c.SecretKey == "minioadmin" || len(c.SecretKey) < 16 {
			return c, errors.New("refusing to start: MinIO credentials must be random (>=16 char secret, not minioadmin) in production")
		}
	}
	return c, nil
}

type minioBackend struct {
	client *minio.Client
	cfg    Config
}

// NewMinIO connects and verifies the credentials by listing buckets.
func NewMinIO(ctx context.Context, c Config) (Backend, error) {
	client, err := minio.New(c.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(c.AccessKey, c.SecretKey, ""), Secure: c.UseSSL})
	if err != nil {
		return nil, fmt.Errorf("minio client: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := client.ListBuckets(ctx); err != nil {
		return nil, fmt.Errorf("minio unreachable or credentials rejected: %w", err)
	}
	return &minioBackend{client: client, cfg: c}, nil
}

func (m *minioBackend) Name() string { return "minio" }

func (m *minioBackend) EnsureBucket(ctx context.Context, name string) error {
	ok, err := m.client.BucketExists(ctx, name)
	if err != nil {
		return err
	}
	if ok {
		return nil
	}
	return m.client.MakeBucket(ctx, name, minio.MakeBucketOptions{})
}

func (m *minioBackend) Put(ctx context.Context, bucket, key, contentType string, r io.Reader, size int64) (string, int64, error) {
	info, err := m.client.PutObject(ctx, bucket, key, r, size, minio.PutObjectOptions{ContentType: contentType})
	if err != nil {
		return "", 0, err
	}
	return info.ETag, info.Size, nil
}

func (m *minioBackend) Get(ctx context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error) {
	obj, err := m.client.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, ObjectInfo{}, err
	}
	st, err := obj.Stat()
	if err != nil {
		_ = obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ObjectInfo{}, ErrNotFound
		}
		return nil, ObjectInfo{}, err
	}
	return obj, ObjectInfo{Key: key, Size: st.Size, ContentType: st.ContentType, ETag: st.ETag, LastModified: st.LastModified}, nil
}

func (m *minioBackend) Delete(ctx context.Context, bucket, key string) error {
	return m.client.RemoveObject(ctx, bucket, key, minio.RemoveObjectOptions{})
}

func (m *minioBackend) DeleteBucket(ctx context.Context, bucket string) error {
	ok, err := m.client.BucketExists(ctx, bucket)
	if err != nil || !ok {
		return err
	}
	objs := m.client.ListObjects(ctx, bucket, minio.ListObjectsOptions{Recursive: true})
	for e := range m.client.RemoveObjects(ctx, bucket, objs, minio.RemoveObjectsOptions{}) {
		if e.Err != nil {
			return e.Err
		}
	}
	return m.client.RemoveBucket(ctx, bucket)
}

func (m *minioBackend) SignedURL(ctx context.Context, bucket, key string, expiry time.Duration, publicBase string) (string, error) {
	client := m.client
	if publicBase != "" {
		// The signature covers the Host header, so sign FOR the public host (offline: fixed region).
		b, err := url.Parse(publicBase)
		if err != nil || b.Host == "" {
			return "", fmt.Errorf("invalid STORAGE_PUBLIC_URL")
		}
		client, err = minio.New(b.Host, &minio.Options{Creds: credentials.NewStaticV4(m.cfg.AccessKey, m.cfg.SecretKey, ""),
			Secure: b.Scheme == "https", Region: "us-east-1"})
		if err != nil {
			return "", err
		}
	}
	u, err := client.PresignedGetObject(ctx, bucket, key, expiry, nil)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

// ------------------------------------------------------------------ in-memory backend (tests only)

type memObj struct {
	data []byte
	ct   string
	at   time.Time
}

// Memory is an in-process Backend for unit tests. It is never constructed from the environment.
type Memory struct {
	mu      sync.Mutex
	Buckets map[string]map[string]memObj
}

func NewMemory() *Memory { return &Memory{Buckets: map[string]map[string]memObj{}} }

func (m *Memory) Name() string { return "memory(test)" }
func (m *Memory) EnsureBucket(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Buckets[name] == nil {
		m.Buckets[name] = map[string]memObj{}
	}
	return nil
}
func (m *Memory) Put(_ context.Context, bucket, key, ct string, r io.Reader, size int64) (string, int64, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return "", 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Buckets[bucket] == nil {
		return "", 0, errors.New("no such bucket")
	}
	m.Buckets[bucket][key] = memObj{data: b, ct: ct, at: time.Now()}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("%x", sum[:8]), int64(len(b)), nil
}
func (m *Memory) Get(_ context.Context, bucket, key string) (io.ReadCloser, ObjectInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.Buckets[bucket][key]
	if !ok {
		return nil, ObjectInfo{}, ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(o.data))), ObjectInfo{Key: key, Size: int64(len(o.data)), ContentType: o.ct, LastModified: o.at}, nil
}
func (m *Memory) Delete(_ context.Context, bucket, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Buckets[bucket], key)
	return nil
}
func (m *Memory) DeleteBucket(_ context.Context, bucket string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Buckets, bucket)
	return nil
}
func (m *Memory) SignedURL(_ context.Context, bucket, key string, expiry time.Duration, publicBase string) (string, error) {
	return fmt.Sprintf("%s/%s/%s?expires=%d", strings.TrimRight(publicBase, "/"), bucket, key, int(expiry.Seconds())), nil
}

// Keys lists the keys of a bucket (tests).
func (m *Memory) Keys(bucket string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ks []string
	for k := range m.Buckets[bucket] {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
