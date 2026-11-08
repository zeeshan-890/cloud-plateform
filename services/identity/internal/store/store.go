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

var (
	ErrNotFound           = errors.New("not found")
	ErrAlreadyExists      = errors.New("already exists")
	ErrAlreadyInitialized = errors.New("platform already initialized")
)

type User struct {
	ID                string     `json:"id"`
	Email             string     `json:"email"`
	Name              string     `json:"name"`
	Username          string     `json:"username,omitempty"`
	ManagedOrgID      *string    `json:"-"`
	MustChangePassword bool      `json:"must_change_password"`
	DisabledAt        *time.Time `json:"-"`
	LastLoginAt       *time.Time `json:"last_login_at,omitempty"`
	PasswordHash      string     `json:"-"`
	IsAdmin           bool       `json:"is_admin"`
	TOTPSecret        *string    `json:"-"` // sealed (enc:v1:...) once upgraded
	TOTPPendingSecret *string    `json:"-"`
	TOTPEnabled       bool       `json:"totp_enabled"`
	CreatedAt         time.Time  `json:"created_at"`
	PasswordChangedAt *time.Time `json:"-"`
}

type Session struct {
	ID         string     `json:"id"`
	UserID     string     `json:"user_id"`
	RefreshJTI string     `json:"-"`
	UserAgent  string     `json:"user_agent"`
	IP         string     `json:"ip"`
	ExpiresAt  time.Time  `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

type PAT struct {
	ID          string     `json:"id"`
	UserID      string     `json:"-"`
	Name        string     `json:"name"`
	TokenHash   string     `json:"-"`
	TokenPrefix string     `json:"-"`
	Scopes      []string   `json:"scopes"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// SettingRow is one persisted platform setting.
type SettingRow struct {
	Key       string
	Value     string // sealed when IsSecret
	IsSecret  bool
	UpdatedAt time.Time
}

// Stats are aggregate counters used by the security report.
type Stats struct {
	Users             int
	Admins            int
	MFAEnabled        int
	AdminsMFAEnabled  int
	BootstrapDone     bool
	LegacyPlainMFA    int
}

// Repo is what the handlers need from persistence (faked in tests).
type Repo interface {
	CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error)
	BootstrapAdmin(ctx context.Context, email, name, passwordHash string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	// GetUserByLogin resolves a username or an e-mail (a login containing '@' is an e-mail).
	GetUserByLogin(ctx context.Context, login string) (*User, error)
	CreateManagedUser(ctx context.Context, orgID, name, username, email, passwordHash string, mustChange bool) (*User, error)
	// The managed-user calls only touch accounts created for that organization.
	GetManaged(ctx context.Context, orgID, userID string) (*User, error)
	UpdateManaged(ctx context.Context, orgID, userID string, name *string, disabled *bool) (*User, error)
	ResetManagedPassword(ctx context.Context, orgID, userID, hash string, mustChange bool) error
	DeleteManaged(ctx context.Context, orgID, userID string) error
	TouchLogin(ctx context.Context, userID string) error
	GetUserByID(ctx context.Context, id string) (*User, error)
	GetUsersByIDs(ctx context.Context, ids []string) ([]User, error)
	ListAdmins(ctx context.Context, limit int) ([]User, error)
	Stats(ctx context.Context) (Stats, error)
	UpdatePassword(ctx context.Context, userID, hash string) error

	CreateSession(ctx context.Context, id, userID, refreshJTI, ua, ip string, expiresAt time.Time) (*Session, error)
	// RotateSession atomically swaps the refresh jti of an active session (single UPDATE ... RETURNING).
	RotateSession(ctx context.Context, oldJTI, newJTI string, newExp time.Time) (*Session, error)
	GetSessionByRefreshJTI(ctx context.Context, jti string) (*Session, error)
	ListSessions(ctx context.Context, userID string) ([]Session, error)
	// RevokeSession revokes one session and returns its refresh jti.
	RevokeSession(ctx context.Context, userID, sessionID string) (string, error)
	// RevokeAllSessions revokes every active session and returns the refresh jtis and session ids.
	RevokeAllSessions(ctx context.Context, userID string) ([]Session, error)

	CreatePAT(ctx context.Context, userID, name, hash, prefix string, scopes []string, expiresAt *time.Time) (*PAT, error)
	ListPATs(ctx context.Context, userID string) ([]PAT, error)
	RevokePAT(ctx context.Context, userID, patID string) error
	LookupPATByHash(ctx context.Context, hash string) (*PAT, *User, error)
	TouchPAT(ctx context.Context, patID string) error

	SetPendingTOTP(ctx context.Context, userID, sealedSecret string) error
	// EnableTOTP atomically promotes the pending secret and replaces the backup codes.
	EnableTOTP(ctx context.Context, userID string, codeHashes []string) error
	DisableTOTP(ctx context.Context, userID string) error
	// ConsumeBackupCode marks the matching unused code as used in ONE statement.
	ConsumeBackupCode(ctx context.Context, userID, codeHash string) (bool, error)
	CountBackupCodes(ctx context.Context, userID string) (int, error)

	IsBootstrapInitialized(ctx context.Context) (bool, error)

	ListSettings(ctx context.Context) ([]SettingRow, error)
	UpsertSettings(ctx context.Context, rows []SettingRow, actor string) error
	DeleteSettings(ctx context.Context, keys []string) error
}

// PG implements Repo on Postgres.
type PG struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *PG {
	return &PG{pool: pool}
}

var _ Repo = (*PG)(nil)

const userCols = `id, COALESCE(email,''), name, COALESCE(username,''), managed_org_id::text, must_change_password, disabled_at, last_login_at, password_hash, is_admin, totp_secret, totp_pending_secret, COALESCE(totp_enabled, FALSE), created_at, password_changed_at`

type scanner interface{ Scan(dest ...any) error }

func scanUser(row scanner) (*User, error) {
	u := &User{}
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.Username, &u.ManagedOrgID, &u.MustChangePassword, &u.DisabledAt, &u.LastLoginAt, &u.PasswordHash, &u.IsAdmin, &u.TOTPSecret, &u.TOTPPendingSecret, &u.TOTPEnabled, &u.CreatedAt, &u.PasswordChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (s *PG) CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error) {
	u := &User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: passwordHash, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, name, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $5)
	`, u.ID, u.Email, u.Name, u.PasswordHash, u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return u, nil
}

// BootstrapAdmin creates the first user as platform admin. It is atomic: the platform_bootstrap
// row is locked FOR UPDATE inside the transaction, so concurrent bootstrap calls serialise and
// only the first succeeds.
func (s *PG) BootstrapAdmin(ctx context.Context, email, name, passwordHash string) (*User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(727001)`); err != nil {
		return nil, err
	}
	var init bool
	err = tx.QueryRow(ctx, `SELECT initialized FROM platform_bootstrap WHERE id='global' FOR UPDATE`).Scan(&init)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err = tx.Exec(ctx, `INSERT INTO platform_bootstrap (id, initialized) VALUES ('global', FALSE) ON CONFLICT DO NOTHING`); err != nil {
			return nil, err
		}
		init = false
	} else if err != nil {
		return nil, err
	}
	if init {
		return nil, ErrAlreadyInitialized
	}
	u := &User{ID: uuid.NewString(), Email: email, Name: name, PasswordHash: passwordHash, IsAdmin: true, CreatedAt: time.Now().UTC()}
	if _, err = tx.Exec(ctx, `
		INSERT INTO users (id, email, name, password_hash, is_admin, created_at, updated_at)
		VALUES ($1, $2, $3, $4, TRUE, $5, $5)
	`, u.ID, u.Email, u.Name, u.PasswordHash, u.CreatedAt); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE platform_bootstrap SET initialized=TRUE, initialized_at=NOW(), initialized_by=$1 WHERE id='global'
	`, u.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *PG) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email=$1`, email))
}

func (s *PG) GetUserByID(ctx context.Context, id string) (*User, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, ErrNotFound
	}
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1`, id))
}

func (s *PG) GetUsersByIDs(ctx context.Context, ids []string) ([]User, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err == nil {
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return []User{}, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE id = ANY($1::text[]::uuid[])`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *PG) ListAdmins(ctx context.Context, limit int) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users WHERE is_admin ORDER BY created_at LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *PG) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE is_admin),
		       COUNT(*) FILTER (WHERE totp_enabled),
		       COUNT(*) FILTER (WHERE is_admin AND totp_enabled),
		       COUNT(*) FILTER (WHERE totp_secret IS NOT NULL AND totp_secret NOT LIKE 'enc:v1:%')
		FROM users`).Scan(&st.Users, &st.Admins, &st.MFAEnabled, &st.AdminsMFAEnabled, &st.LegacyPlainMFA)
	if err != nil {
		return st, err
	}
	st.BootstrapDone, err = s.IsBootstrapInitialized(ctx)
	return st, err
}

func (s *PG) UpdatePassword(ctx context.Context, userID, hash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$1, must_change_password=FALSE, password_changed_at=NOW(), updated_at=NOW() WHERE id=$2`, hash, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) CreateSession(ctx context.Context, id, userID, refreshJTI, ua, ip string, expiresAt time.Time) (*Session, error) {
	if id == "" {
		id = uuid.NewString()
	}
	sess := &Session{ID: id, UserID: userID, RefreshJTI: refreshJTI, UserAgent: truncate(ua, 300), IP: truncate(ip, 64), ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, refresh_jti, user_agent, ip, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, sess.ID, sess.UserID, sess.RefreshJTI, sess.UserAgent, sess.IP, sess.ExpiresAt, sess.CreatedAt)
	return sess, err
}

func (s *PG) RotateSession(ctx context.Context, oldJTI, newJTI string, newExp time.Time) (*Session, error) {
	sess := &Session{}
	err := s.pool.QueryRow(ctx, `
		UPDATE sessions SET refresh_jti=$2, expires_at=$3
		WHERE refresh_jti=$1 AND revoked_at IS NULL AND expires_at > NOW()
		RETURNING id, user_id, refresh_jti, COALESCE(user_agent,''), COALESCE(ip,''), expires_at, revoked_at, created_at
	`, oldJTI, newJTI, newExp).Scan(&sess.ID, &sess.UserID, &sess.RefreshJTI, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.RevokedAt, &sess.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

func (s *PG) GetSessionByRefreshJTI(ctx context.Context, jti string) (*Session, error) {
	sess := &Session{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, refresh_jti, COALESCE(user_agent,''), COALESCE(ip,''), expires_at, revoked_at, created_at
		FROM sessions WHERE refresh_jti=$1
	`, jti).Scan(&sess.ID, &sess.UserID, &sess.RefreshJTI, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.RevokedAt, &sess.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return sess, err
}

func (s *PG) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, refresh_jti, COALESCE(user_agent,''), COALESCE(ip,''), expires_at, revoked_at, created_at
		FROM sessions
		WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC LIMIT 200
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.RefreshJTI, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.RevokedAt, &sess.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *PG) RevokeSession(ctx context.Context, userID, sessionID string) (string, error) {
	if _, err := uuid.Parse(sessionID); err != nil {
		return "", ErrNotFound
	}
	var jti string
	err := s.pool.QueryRow(ctx, `
		UPDATE sessions SET revoked_at=NOW() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL RETURNING refresh_jti
	`, sessionID, userID).Scan(&jti)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return jti, err
}

func (s *PG) RevokeAllSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE sessions SET revoked_at=NOW() WHERE user_id=$1 AND revoked_at IS NULL RETURNING id, refresh_jti
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.RefreshJTI); err != nil {
			return nil, err
		}
		sess.UserID = userID
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *PG) CreatePAT(ctx context.Context, userID, name, hash, prefix string, scopes []string, expiresAt *time.Time) (*PAT, error) {
	if scopes == nil {
		scopes = []string{}
	}
	p := &PAT{ID: uuid.NewString(), UserID: userID, Name: name, TokenHash: hash, TokenPrefix: prefix, Scopes: scopes, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO personal_access_tokens (id, user_id, name, token_hash, token_prefix, scopes, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, p.ID, p.UserID, p.Name, p.TokenHash, p.TokenPrefix, p.Scopes, p.ExpiresAt, p.CreatedAt)
	return p, err
}

func (s *PG) ListPATs(ctx context.Context, userID string) ([]PAT, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, token_prefix, scopes, last_used_at, expires_at, created_at
		FROM personal_access_tokens
		WHERE user_id=$1 AND revoked_at IS NULL
		ORDER BY created_at DESC LIMIT 200
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PAT{}
	for rows.Next() {
		var p PAT
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.TokenPrefix, &p.Scopes, &p.LastUsedAt, &p.ExpiresAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *PG) RevokePAT(ctx context.Context, userID, patID string) error {
	if _, err := uuid.Parse(patID); err != nil {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE personal_access_tokens SET revoked_at=NOW() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL
	`, patID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// LookupPATByHash returns an active (not revoked) PAT and its user; expiry is checked by the caller.
func (s *PG) LookupPATByHash(ctx context.Context, hash string) (*PAT, *User, error) {
	p := &PAT{}
	u := &User{}
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.user_id, p.name, p.token_prefix, p.scopes, p.last_used_at, p.expires_at, p.created_at,
		       u.id, COALESCE(u.email,''), u.name, u.is_admin, u.managed_org_id::text, u.disabled_at, u.created_at
		FROM personal_access_tokens p
		JOIN users u ON u.id = p.user_id
		WHERE p.token_hash=$1 AND p.revoked_at IS NULL
	`, hash).Scan(
		&p.ID, &p.UserID, &p.Name, &p.TokenPrefix, &p.Scopes, &p.LastUsedAt, &p.ExpiresAt, &p.CreatedAt,
		&u.ID, &u.Email, &u.Name, &u.IsAdmin, &u.ManagedOrgID, &u.DisabledAt, &u.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return p, u, nil
}

// TouchPAT records last use, at most once per minute per token.
func (s *PG) TouchPAT(ctx context.Context, patID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE personal_access_tokens SET last_used_at=NOW()
		WHERE id=$1 AND (last_used_at IS NULL OR last_used_at < NOW() - INTERVAL '1 minute')
	`, patID)
	return err
}

func (s *PG) SetPendingTOTP(ctx context.Context, userID, sealedSecret string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET totp_pending_secret=$1 WHERE id=$2`, sealedSecret, userID)
	return err
}

func (s *PG) EnableTOTP(ctx context.Context, userID string, codeHashes []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `
		UPDATE users SET totp_secret=totp_pending_secret, totp_pending_secret=NULL, totp_enabled=TRUE, totp_backup_codes='[]'::jsonb
		WHERE id=$1 AND totp_pending_secret IS NOT NULL
	`, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err = tx.Exec(ctx, `DELETE FROM identity_backup_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, h := range codeHashes {
		if _, err = tx.Exec(ctx, `INSERT INTO identity_backup_codes (user_id, code_hash) VALUES ($1,$2)`, userID, h); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PG) DisableTOTP(ctx context.Context, userID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE users SET totp_enabled=FALSE, totp_secret=NULL, totp_pending_secret=NULL, totp_backup_codes='[]'::jsonb WHERE id=$1`, userID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM identity_backup_codes WHERE user_id=$1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PG) ConsumeBackupCode(ctx context.Context, userID, codeHash string) (bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		UPDATE identity_backup_codes SET used_at=NOW()
		WHERE user_id=$1 AND code_hash=$2 AND used_at IS NULL
		RETURNING id
	`, userID, codeHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *PG) CountBackupCodes(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM identity_backup_codes WHERE user_id=$1 AND used_at IS NULL`, userID).Scan(&n)
	return n, err
}

func (s *PG) IsBootstrapInitialized(ctx context.Context) (bool, error) {
	var init bool
	err := s.pool.QueryRow(ctx, `SELECT initialized FROM platform_bootstrap WHERE id='global'`).Scan(&init)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return init, err
}

func (s *PG) ListSettings(ctx context.Context) ([]SettingRow, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value, is_secret, updated_at FROM platform_settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SettingRow{}
	for rows.Next() {
		var r SettingRow
		if err := rows.Scan(&r.Key, &r.Value, &r.IsSecret, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *PG) UpsertSettings(ctx context.Context, rows []SettingRow, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var by any
	if _, perr := uuid.Parse(actor); perr == nil {
		by = actor
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `
			INSERT INTO platform_settings (key, value, is_secret, updated_at, updated_by) VALUES ($1,$2,$3,NOW(),$4)
			ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, is_secret=EXCLUDED.is_secret, updated_at=NOW(), updated_by=EXCLUDED.updated_by
		`, r.Key, r.Value, r.IsSecret, by); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PG) DeleteSettings(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM platform_settings WHERE key = ANY($1)`, keys)
	return err
}

// LegacyMFA is a plaintext TOTP row found by the startup upgrade.
type LegacyMFA struct {
	UserID  string
	Secret  string
	Enabled bool
	Codes   []string
}

// UpgradeLegacyMFA finds rows written by migration 002 (plaintext seed / plaintext backup codes)
// and rewrites them using seal (for the seed) and hash (for backup codes). Idempotent.
func (s *PG) UpgradeLegacyMFA(ctx context.Context, seal func(userID, secret string) (string, error), hash func(userID, code string) string) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, totp_secret, COALESCE(totp_enabled,FALSE), COALESCE(totp_backup_codes,'[]'::jsonb)
		FROM users
		WHERE (totp_secret IS NOT NULL AND totp_secret NOT LIKE 'enc:v1:%')
		   OR (totp_backup_codes IS NOT NULL AND totp_backup_codes <> '[]'::jsonb)`)
	if err != nil {
		return 0, err
	}
	var legacy []LegacyMFA
	for rows.Next() {
		var l LegacyMFA
		var secret *string
		var raw []byte
		if err := rows.Scan(&l.UserID, &secret, &l.Enabled, &raw); err != nil {
			rows.Close()
			return 0, err
		}
		if secret != nil {
			l.Secret = *secret
		}
		_ = json.Unmarshal(raw, &l.Codes)
		legacy = append(legacy, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, l := range legacy {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return n, err
		}
		if l.Secret != "" && !strings.HasPrefix(l.Secret, "enc:v1:") {
			sealed, err := seal(l.UserID, l.Secret)
			if err != nil {
				tx.Rollback(ctx)
				return n, err
			}
			if l.Enabled {
				_, err = tx.Exec(ctx, `UPDATE users SET totp_secret=$1 WHERE id=$2`, sealed, l.UserID)
			} else {
				// An un-confirmed setup becomes a pending enrolment.
				_, err = tx.Exec(ctx, `UPDATE users SET totp_secret=NULL, totp_pending_secret=$1 WHERE id=$2`, sealed, l.UserID)
			}
			if err != nil {
				tx.Rollback(ctx)
				return n, err
			}
		}
		for _, c := range l.Codes {
			if _, err := tx.Exec(ctx, `INSERT INTO identity_backup_codes (user_id, code_hash) VALUES ($1,$2) ON CONFLICT DO NOTHING`, l.UserID, hash(l.UserID, c)); err != nil {
				tx.Rollback(ctx)
				return n, err
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET totp_backup_codes='[]'::jsonb WHERE id=$1`, l.UserID); err != nil {
			tx.Rollback(ctx)
			return n, err
		}
		if err := tx.Commit(ctx); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func isUniqueViolation(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint"))
}
