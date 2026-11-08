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
	Value          string    `json:"value,omitempty"` // only ever set for admin+ reveal
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
	q := `
		SELECT id, org_id, project_id, environment, name, current_version, value_hint, created_by, created_at, updated_at
		FROM secrets WHERE org_id=$1 AND project_id=$2`
	args := []any{orgID, projectID}
	if env != "" && env != "all" {
		q += ` AND environment=$3`
		args = append(args, env)
	}
	q += ` ORDER BY environment ASC, name ASC`
	rows, err := s.pool.Query(ctx, q, args...)
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
	if _, perr := uuid.Parse(actorID); actorID != "" && perr == nil { // service actors ("addon") are not user ids
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
	return s.UpdateSecretWithRename(ctx, meta, meta.Name, hint, ciphertext, nonce, keyVersion, actorID)
}

func (s *Store) UpdateSecretWithRename(ctx context.Context, meta *SecretMeta, newName, hint string, ciphertext, nonce []byte, keyVersion int, actorID string) (*SecretMeta, error) {
	now := time.Now().UTC()
	var actor *string
	if _, perr := uuid.Parse(actorID); actorID != "" && perr == nil { // service actors ("addon") are not user ids
		actor = &actorID
	}
	nameToSet := meta.Name
	if newName != "" {
		nameToSet = newName
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	next := meta.CurrentVersion
	if len(ciphertext) > 0 {
		next = meta.CurrentVersion + 1
		_, err = tx.Exec(ctx, `
			INSERT INTO secret_versions (id, secret_id, version, ciphertext, nonce, key_version, created_by, created_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		`, uuid.NewString(), meta.ID, next, ciphertext, nonce, keyVersion, actor, now)
		if err != nil {
			return nil, err
		}
	}

	hintToSet := meta.ValueHint
	if hint != "" {
		hintToSet = hint
	}

	_, err = tx.Exec(ctx, `
		UPDATE secrets SET name=$2, current_version=$3, value_hint=$4, updated_at=$5 WHERE id=$1
	`, meta.ID, nameToSet, next, hintToSet, now)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, ErrConflict
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	meta.Name = nameToSet
	meta.CurrentVersion = next
	meta.ValueHint = hintToSet
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

func (s *Store) GetCiphertext(ctx context.Context, secretID string, version int) (ciphertext, nonce []byte, keyVersion int, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT ciphertext, nonce, key_version FROM secret_versions WHERE secret_id=$1 AND version=$2
	`, secretID, version).Scan(&ciphertext, &nonce, &keyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, 0, ErrNotFound
	}
	return ciphertext, nonce, keyVersion, err
}

func (s *Store) Audit(ctx context.Context, orgID, projectID, env, secretName, action, actorID, requestID string, meta map[string]any) {
	if meta == nil {
		meta = map[string]any{}
	}
	var actor any
	if actorID != "" {
		if _, err := uuid.Parse(actorID); err == nil {
			actor = actorID
		} else {
			meta["actor"] = actorID // service callers (e.g. "addon") are not user ids
		}
	}
	b, _ := json.Marshal(meta)
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO secret_audit (id, org_id, project_id, environment, secret_name, action, actor_id, metadata, request_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
	`, uuid.NewString(), orgID, projectID, env, secretName, action, actor, b, requestID, time.Now().UTC())
}

// EnvRow is one current secret value (still encrypted) used to build an environment.
type EnvRow struct {
	Environment string
	Name        string
	Ciphertext  []byte
	Nonce       []byte
	KeyVersion  int
}

// EnvRows returns the current version of every secret of the project in the given environments.
func (s *Store) EnvRows(ctx context.Context, orgID, projectID string, envs []string) ([]EnvRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.environment, s.name, v.ciphertext, v.nonce, v.key_version
		FROM secrets s JOIN secret_versions v ON v.secret_id = s.id AND v.version = s.current_version
		WHERE s.org_id=$1 AND s.project_id=$2 AND s.environment = ANY($3)
	`, orgID, projectID, envs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EnvRow
	for rows.Next() {
		var r EnvRow
		if err := rows.Scan(&r.Environment, &r.Name, &r.Ciphertext, &r.Nonce, &r.KeyVersion); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AuditEntry is one audit row (never contains secret values).
type AuditEntry struct {
	ID          string          `json:"id"`
	Environment string          `json:"environment"`
	SecretName  string          `json:"secret_name"`
	Action      string          `json:"action"`
	ActorID     *string         `json:"actor_id,omitempty"`
	Metadata    json.RawMessage `json:"metadata"`
	RequestID   string          `json:"request_id,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ListAudit returns a page of the project's audit trail, newest first.
func (s *Store) ListAudit(ctx context.Context, orgID, projectID string, limit, offset int) ([]AuditEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, environment, secret_name, action, actor_id, metadata, request_id, created_at
		FROM secret_audit WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC LIMIT $3 OFFSET $4
	`, orgID, projectID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err := rows.Scan(&a.ID, &a.Environment, &a.SecretName, &a.Action, &a.ActorID, &a.Metadata, &a.RequestID, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeleteProject removes every secret, environment and audit row of a project.
func (s *Store) DeleteProject(ctx context.Context, projectID string) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `DELETE FROM secrets WHERE project_id=$1`, projectID) // versions cascade
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_environments WHERE project_id=$1`, projectID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM secret_audit WHERE project_id=$1`, projectID); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}

// VersionRow is one stored ciphertext for re-encryption.
type VersionRow struct {
	ID         string
	Ciphertext []byte
	Nonce      []byte
	KeyVersion int
}

// OutdatedVersions returns up to limit versions encrypted with a key version other than current.
func (s *Store) OutdatedVersions(ctx context.Context, currentKeyVersion, limit int, projectID string) ([]VersionRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v.id, v.ciphertext, v.nonce, v.key_version
		FROM secret_versions v JOIN secrets s ON s.id = v.secret_id
		WHERE v.key_version <> $1 AND ($3 = '' OR s.project_id::text = $3)
		ORDER BY v.created_at LIMIT $2
	`, currentKeyVersion, limit, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionRow
	for rows.Next() {
		var r VersionRow
		if err := rows.Scan(&r.ID, &r.Ciphertext, &r.Nonce, &r.KeyVersion); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetCiphertext replaces a version's ciphertext after re-encryption.
func (s *Store) SetCiphertext(ctx context.Context, versionID string, ct, nonce []byte, keyVersion int) error {
	_, err := s.pool.Exec(ctx, `UPDATE secret_versions SET ciphertext=$2, nonce=$3, key_version=$4 WHERE id=$1`, versionID, ct, nonce, keyVersion)
	return err
}
