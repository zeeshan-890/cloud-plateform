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
	ErrForbidden     = errors.New("forbidden")
)

type Org struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Member struct {
	UserID   string    `json:"user_id"`
	Email    string    `json:"email"`
	Name     string    `json:"name"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joined_at"`
}

type Invite struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateOrg(ctx context.Context, name, slug, ownerID string) (*Org, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	org := &Org{ID: uuid.NewString(), Name: name, Slug: slug, Role: "owner", CreatedAt: time.Now().UTC()}
	_, err = tx.Exec(ctx, `
		INSERT INTO organizations (id, name, slug, created_at, updated_at) VALUES ($1,$2,$3,$4,$4)
	`, org.ID, org.Name, org.Slug, org.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role, joined_at) VALUES ($1,$2,'owner',$3)
	`, org.ID, ownerID, org.CreatedAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return org, nil
}

func (s *Store) ListOrgsForUser(ctx context.Context, userID string) ([]Org, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.name, o.slug, m.role, o.created_at
		FROM organizations o
		JOIN org_members m ON m.org_id = o.id
		WHERE m.user_id = $1
		ORDER BY o.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Role, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	if out == nil {
		out = []Org{}
	}
	return out, rows.Err()
}

func (s *Store) GetOrg(ctx context.Context, orgID, userID string) (*Org, error) {
	o := &Org{}
	err := s.pool.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, m.role, o.created_at
		FROM organizations o
		JOIN org_members m ON m.org_id = o.id
		WHERE o.id=$1 AND m.user_id=$2
	`, orgID, userID).Scan(&o.ID, &o.Name, &o.Slug, &o.Role, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) MemberRole(ctx context.Context, orgID, userID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (s *Store) CreateInvite(ctx context.Context, orgID, email, role, token, invitedBy string, expiresAt time.Time) (*Invite, error) {
	inv := &Invite{
		ID: uuid.NewString(), OrgID: orgID, Email: email, Role: role, Token: token, ExpiresAt: expiresAt,
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO org_invites (id, org_id, email, role, token, invited_by, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,NOW())
	`, inv.ID, inv.OrgID, inv.Email, inv.Role, inv.Token, invitedBy, inv.ExpiresAt)
	return inv, err
}

func (s *Store) AcceptInvite(ctx context.Context, token, userID, userEmail string) (*Org, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	var inv Invite
	var accepted *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, org_id, email, role, token, expires_at, accepted_at
		FROM org_invites WHERE token=$1 FOR UPDATE
	`, token).Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &inv.Token, &inv.ExpiresAt, &accepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if accepted != nil {
		return nil, "", ErrAlreadyExists
	}
	if time.Now().After(inv.ExpiresAt) {
		return nil, "", ErrNotFound
	}
	if inv.Email != "" && userEmail != "" && inv.Email != userEmail {
		return nil, "", ErrForbidden
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role, joined_at)
		VALUES ($1,$2,$3,NOW())
		ON CONFLICT (org_id, user_id) DO UPDATE SET role=EXCLUDED.role
	`, inv.OrgID, userID, inv.Role)
	if err != nil {
		return nil, "", err
	}
	_, err = tx.Exec(ctx, `UPDATE org_invites SET accepted_at=NOW() WHERE id=$1`, inv.ID)
	if err != nil {
		return nil, "", err
	}

	org := &Org{}
	err = tx.QueryRow(ctx, `SELECT id, name, slug, created_at FROM organizations WHERE id=$1`, inv.OrgID).
		Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt)
	if err != nil {
		return nil, "", err
	}
	org.Role = inv.Role
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return org, inv.Role, nil
}

func (s *Store) ListMembers(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, role, joined_at FROM org_members WHERE org_id=$1 ORDER BY joined_at
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []Member{}
	}
	return out, rows.Err()
}

func isUnique(err error) bool {
	return err != nil && (contains(err.Error(), "duplicate key") || contains(err.Error(), "unique constraint"))
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
