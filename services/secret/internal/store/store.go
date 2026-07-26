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

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

var ValidEnvironments = map[string]bool{
	"development": true,
	"preview":     true,
	"staging":     true,
	"production":  true,
}

type Environment struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type SecretMeta struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	ProjectID      string    `json:"project_id"`
	Environment    string    `json:"environment"`
	Name           string    `json:"name"`
	CurrentVersion int       `json:"current_version"`
	ValueHint      string    `json:"value_hint"`
	CreatedBy      *string   `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SecretVersionMeta struct {
	ID         string    `json:"id"`
	SecretID   string    `json:"secret_id"`
	Version    int       `json:"version"`
	KeyVersion int       `json:"key_version"`
	CreatedBy  *string   `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) EnsureDefaultEnvironments(ctx context.Context, orgID, projectID string) error {
	for _, name := range []string{"development", "preview", "staging", "production"} {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO project_environments (id, org_id, project_id, name, created_at)
			VALUES ($1,$2,$3,$4,$5)
			ON CONFLICT (project_id, name) DO NOTHING
		`, uuid.NewString(), orgID, projectID, name, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListEnvironments(ctx context.Context, orgID, projectID string) ([]Environment, error) {
	if err := s.EnsureDefaultEnvironments(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, name, created_at
		FROM project_environments WHERE org_id=$1 AND project_id=$2
		ORDER BY CASE name
			WHEN 'development' THEN 1 WHEN 'preview' THEN 2
			WHEN 'staging' THEN 3 WHEN 'production' THEN 4 ELSE 5 END
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Environment
	for rows.Next() {
		var e Environment
		if err := rows.Scan(&e.ID, &e.OrgID, &e.ProjectID, &e.Name, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if out == nil {
		out = []Environment{}
	}
	return out, rows.Err()
}

func (s *Store) CreateEnvironment(ctx context.Context, orgID, projectID, name string) (*Environment, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidEnvironments[name] {
		return nil, ErrConflict
	}
	e := &Environment{
		ID: uuid.NewString(), OrgID: orgID, ProjectID: projectID, Name: name, CreatedAt: time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_environments (id, org_id, project_id, name, created_at)
		VALUES ($1,$2,$3,$4,$5)
	`, e.ID, e.OrgID, e.ProjectID, e.Name, e.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, ErrConflict
		}
		return nil, err
	}
	return e, nil
}

func (s *Store) ListSecrets(ctx context.Context, orgID, projectID, env string) ([]SecretMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, environment, name, current_version, value_hint, created_by, created_at, updated_at
		FROM secrets WHERE org_id=$1 AND project_id=$2 AND environment=$3
		ORDER BY name ASC
	`, orgID, projectID, env)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretMeta
	for rows.Next() {
		var m SecretMeta
		if err := rows.Scan(&m.ID, &m.OrgID, &m.ProjectID, &m.Environment, &m.Name, &m.CurrentVersion,
			&m.ValueHint, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []SecretMeta{}
	}
	return out, rows.Err()
}

func (s *Store) GetSecret(ctx context.Context, orgID, projectID, env, name string) (*SecretMeta, error) {
	m := &SecretMeta{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, environment, name, current_version, value_hint, created_by, created_at, updated_at
		FROM secrets WHERE org_id=$1 AND project_id=$2 AND environment=$3 AND name=$4
	`, orgID, projectID, env, name).Scan(
		&m.ID, &m.OrgID, &m.ProjectID, &m.Environment, &m.Name, &m.CurrentVersion,
		&m.ValueHint, &m.CreatedBy, &m.CreatedAt, &m.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return m, err
}

func (s *Store) CreateSecret(ctx context.Context, orgID, projectID, env, name, hint string, ciphertext, nonce []byte, keyVersion int, actorID string) (*SecretMeta, error) {
	now := time.Now().UTC()
	id := uuid.NewString()
	verID := uuid.NewString()
	var actor *string
	if actorID != "" {
		actor = &actorID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO secrets (id, org_id, project_id, environment, name, current_version, value_hint, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,1,$6,$7,$8,$8)
	`, id, orgID, projectID, env, name, hint, actor, now)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			return nil, ErrConflict
		}
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO secret_versions (id, secret_id, version, ciphertext, nonce, key_version, created_by, created_at)
		VALUES ($1,$2,1,$3,$4,$5,$6,$7)
	`, verID, id, ciphertext, nonce, keyVersion, actor, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &SecretMeta{
		ID: id, OrgID: orgID, ProjectID: projectID, Environment: env, Name: name,
		CurrentVersion: 1, ValueHint: hint, CreatedBy: actor, CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *Store) UpdateSecret(ctx context.Context, meta *SecretMeta, hint string, ciphertext, nonce []byte, keyVersion int, actorID string) (*SecretMeta, error) {
	now := time.Now().UTC()
	next := meta.CurrentVersion + 1
	var actor *string
	if actorID != "" {
		actor = &actorID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO secret_versions (id, secret_id, version, ciphertext, nonce, key_version, created_by, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, uuid.NewString(), meta.ID, next, ciphertext, nonce, keyVersion, actor, now)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE secrets SET current_version=$2, value_hint=$3, updated_at=$4 WHERE id=$1
	`, meta.ID, next, hint, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	meta.CurrentVersion = next
	meta.ValueHint = hint
	meta.UpdatedAt = now
	return meta, nil
}

func (s *Store) DeleteSecret(ctx context.Context, orgID, projectID, env, name string) error {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM secrets WHERE org_id=$1 AND project_id=$2 AND environment=$3 AND name=$4
	`, orgID, projectID, env, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ListVersions(ctx context.Context, secretID string) ([]SecretVersionMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, secret_id, version, key_version, created_by, created_at
		FROM secret_versions WHERE secret_id=$1 ORDER BY version DESC
	`, secretID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretVersionMeta
	for rows.Next() {
		var v SecretVersionMeta
		if err := rows.Scan(&v.ID, &v.SecretID, &v.Version, &v.KeyVersion, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if out == nil {
		out = []SecretVersionMeta{}
	}
	return out, rows.Err()
}

func (s *Store) GetCiphertext(ctx context.Context, secretID string, version int) (ciphertext, nonce []byte, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT ciphertext, nonce FROM secret_versions WHERE secret_id=$1 AND version=$2
	`, secretID, version).Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	return ciphertext, nonce, err
}

func (s *Store) Audit(ctx context.Context, orgID, projectID, env, secretName, action, actorID, requestID string, meta map[string]any) {
	if meta == nil {
		meta = map[string]any{}
	}
	b, _ := json.Marshal(meta)
	var actor any
	if actorID != "" {
		actor = actorID
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO secret_audit (id, org_id, project_id, environment, secret_name, action, actor_id, metadata, request_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, uuid.NewString(), orgID, projectID, env, secretName, action, actor, b, requestID, time.Now().UTC())
}
