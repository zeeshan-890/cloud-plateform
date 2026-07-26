package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
)

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	Name         string    `json:"name"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
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
	CreatedAt   time.Time  `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error) {
	u := &User{
		ID:           uuid.NewString(),
		Email:        email,
		Name:         name,
		PasswordHash: passwordHash,
		CreatedAt:    time.Now().UTC(),
	}
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

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, created_at FROM users WHERE email=$1
	`, email).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*User, error) {
	u := &User{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, name, password_hash, created_at FROM users WHERE id=$1
	`, id).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func (s *Store) CreateSession(ctx context.Context, userID, refreshJTI, ua, ip string, expiresAt time.Time) (*Session, error) {
	sess := &Session{
		ID:         uuid.NewString(),
		UserID:     userID,
		RefreshJTI: refreshJTI,
		UserAgent:  ua,
		IP:         ip,
		ExpiresAt:  expiresAt,
		CreatedAt:  time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, refresh_jti, user_agent, ip, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, sess.ID, sess.UserID, sess.RefreshJTI, sess.UserAgent, sess.IP, sess.ExpiresAt, sess.CreatedAt)
	return sess, err
}

func (s *Store) GetSessionByRefreshJTI(ctx context.Context, jti string) (*Session, error) {
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

func (s *Store) ListSessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, refresh_jti, COALESCE(user_agent,''), COALESCE(ip,''), expires_at, revoked_at, created_at
		FROM sessions
		WHERE user_id=$1 AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var sess Session
		if err := rows.Scan(&sess.ID, &sess.UserID, &sess.RefreshJTI, &sess.UserAgent, &sess.IP, &sess.ExpiresAt, &sess.RevokedAt, &sess.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	if out == nil {
		out = []Session{}
	}
	return out, rows.Err()
}

func (s *Store) RevokeSession(ctx context.Context, userID, sessionID string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at=NOW() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL
	`, sessionID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeSessionByJTI(ctx context.Context, jti string) error {
	_, err := s.pool.Exec(ctx, `UPDATE sessions SET revoked_at=NOW() WHERE refresh_jti=$1 AND revoked_at IS NULL`, jti)
	return err
}

func (s *Store) CreatePAT(ctx context.Context, userID, name, hash, prefix string, scopes []string) (*PAT, error) {
	if scopes == nil {
		scopes = []string{}
	}
	p := &PAT{
		ID:          uuid.NewString(),
		UserID:      userID,
		Name:        name,
		TokenHash:   hash,
		TokenPrefix: prefix,
		Scopes:      scopes,
		CreatedAt:   time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO personal_access_tokens (id, user_id, name, token_hash, token_prefix, scopes, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, p.ID, p.UserID, p.Name, p.TokenHash, p.TokenPrefix, p.Scopes, p.CreatedAt)
	return p, err
}

func (s *Store) ListPATs(ctx context.Context, userID string) ([]PAT, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, name, token_hash, token_prefix, scopes, last_used_at, created_at
		FROM personal_access_tokens
		WHERE user_id=$1 AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PAT
	for rows.Next() {
		var p PAT
		if err := rows.Scan(&p.ID, &p.UserID, &p.Name, &p.TokenHash, &p.TokenPrefix, &p.Scopes, &p.LastUsedAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []PAT{}
	}
	return out, rows.Err()
}

func (s *Store) RevokePAT(ctx context.Context, userID, patID string) error {
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

// LookupPATByHash returns an active PAT (and its user) for the given token hash.
func (s *Store) LookupPATByHash(ctx context.Context, hash string) (*PAT, *User, error) {
	p := &PAT{}
	u := &User{}
	err := s.pool.QueryRow(ctx, `
		SELECT p.id, p.user_id, p.name, p.token_hash, p.token_prefix, p.scopes, p.last_used_at, p.created_at,
		       u.id, u.email, u.name, u.created_at
		FROM personal_access_tokens p
		JOIN users u ON u.id = p.user_id
		WHERE p.token_hash=$1 AND p.revoked_at IS NULL
	`, hash).Scan(
		&p.ID, &p.UserID, &p.Name, &p.TokenHash, &p.TokenPrefix, &p.Scopes, &p.LastUsedAt, &p.CreatedAt,
		&u.ID, &u.Email, &u.Name, &u.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	return p, u, nil
}

func (s *Store) TouchPAT(ctx context.Context, patID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE personal_access_tokens SET last_used_at=NOW() WHERE id=$1
	`, patID)
	return err
}

func isUniqueViolation(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
