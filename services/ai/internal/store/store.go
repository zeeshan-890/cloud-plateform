package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

// Config is one organisation's AI configuration (EncryptedKey is nonce||ciphertext).
type Config struct {
	OrgID        string
	Provider     string
	Model        string
	BaseURL      string
	EncryptedKey []byte
	KeyLast4     string
	UpdatedBy    string
	UpdatedAt    time.Time
}

// Store persists per-org configuration.
type Store interface {
	Get(ctx context.Context, orgID string) (*Config, error)
	Put(ctx context.Context, c *Config) error
	Delete(ctx context.Context, orgID string) error
}

type PG struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

func (s *PG) Get(ctx context.Context, orgID string) (*Config, error) {
	c := &Config{}
	var by *string
	err := s.pool.QueryRow(ctx, `SELECT org_id::text, provider, model, base_url, encrypted_key, key_last4, updated_by::text, updated_at FROM ai_config WHERE org_id=$1::uuid`, orgID).
		Scan(&c.OrgID, &c.Provider, &c.Model, &c.BaseURL, &c.EncryptedKey, &c.KeyLast4, &by, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if by != nil {
		c.UpdatedBy = *by
	}
	return c, err
}

func (s *PG) Put(ctx context.Context, c *Config) error {
	c.UpdatedAt = time.Now().UTC()
	var by any
	if c.UpdatedBy != "" {
		by = c.UpdatedBy
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO ai_config (org_id, provider, model, base_url, encrypted_key, key_last4, updated_by, updated_at)
		VALUES ($1::uuid,$2,$3,$4,$5,$6,$7::uuid,$8)
		ON CONFLICT (org_id) DO UPDATE SET provider=EXCLUDED.provider, model=EXCLUDED.model, base_url=EXCLUDED.base_url,
			encrypted_key=EXCLUDED.encrypted_key, key_last4=EXCLUDED.key_last4, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
		c.OrgID, c.Provider, c.Model, c.BaseURL, c.EncryptedKey, c.KeyLast4, by, c.UpdatedAt)
	return err
}

func (s *PG) Delete(ctx context.Context, orgID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM ai_config WHERE org_id=$1::uuid`, orgID)
	return err
}
