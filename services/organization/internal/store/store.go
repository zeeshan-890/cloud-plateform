package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrForbidden     = errors.New("forbidden")
	// ErrLastOwner is returned when an operation would leave an organization without an owner.
	ErrLastOwner = errors.New("organization must keep at least one owner")
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
	Managed  bool      `json:"managed"`
	JoinedAt time.Time `json:"joined_at"`
}

// Invite never carries the token: only its hash is stored.
type Invite struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	InvitedBy string    `json:"invited_by,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// HashToken returns the storage form of an invite token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Repo is the persistence surface used by the handlers (faked in tests).
type Repo interface {
	CreateOrg(ctx context.Context, name, slug, ownerID string) (*Org, error)
	ListOrgsForUser(ctx context.Context, userID string) ([]Org, error)
	GetOrg(ctx context.Context, orgID, userID string) (*Org, error)
	GetOrgByID(ctx context.Context, orgID string) (*Org, error)
	MemberRole(ctx context.Context, orgID, userID string) (string, error)
	DeleteOrg(ctx context.Context, orgID string) error

	CreateInvite(ctx context.Context, orgID, email, role, tokenHash, invitedBy string, expiresAt time.Time) (*Invite, error)
	CountPendingInvites(ctx context.Context, orgID string) (int, error)
	ListInvites(ctx context.Context, orgID string) ([]Invite, error)
	RevokeInvite(ctx context.Context, orgID, inviteID string) error
	CheckInvite(ctx context.Context, tokenHash, email string) (*Invite, error)
	AcceptInvite(ctx context.Context, tokenHash, userID, userEmail string) (*Org, string, error)

	ListMembers(ctx context.Context, orgID string) ([]Member, error)
	CountMembers(ctx context.Context, orgID string) (int, error)
	// SetMemberRole and RemoveMember enforce the last-owner invariant atomically.
	SetMemberRole(ctx context.Context, orgID, userID, role string) error
	RemoveMember(ctx context.Context, orgID, userID string) error

	// Admin-managed users and per-project grants.
	AddMember(ctx context.Context, orgID, userID, role string, managed bool) error
	IsManagedMember(ctx context.Context, orgID, userID string) (bool, error)
	GetGrant(ctx context.Context, orgID, projectID, userID string) (*Grant, error)
	ListGrantsForUser(ctx context.Context, orgID, userID string) ([]Grant, error)
	ListGrantsForOrg(ctx context.Context, orgID string) ([]Grant, error)
	ReplaceGrants(ctx context.Context, orgID, userID, grantedBy string, grants []Grant) error
	DeleteGrantsForProject(ctx context.Context, projectID string) error
}

type PG struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *PG { return &PG{pool: pool} }

var _ Repo = (*PG)(nil)

func (s *PG) CreateOrg(ctx context.Context, name, slug, ownerID string) (*Org, error) {
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

func (s *PG) ListOrgsForUser(ctx context.Context, userID string) ([]Org, error) {
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
	out := []Org{}
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Role, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *PG) GetOrg(ctx context.Context, orgID, userID string) (*Org, error) {
	if _, err := uuid.Parse(orgID); err != nil {
		return nil, ErrNotFound
	}
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

func (s *PG) GetOrgByID(ctx context.Context, orgID string) (*Org, error) {
	if _, err := uuid.Parse(orgID); err != nil {
		return nil, ErrNotFound
	}
	o := &Org{}
	err := s.pool.QueryRow(ctx, `SELECT id, name, slug, created_at FROM organizations WHERE id=$1`, orgID).
		Scan(&o.ID, &o.Name, &o.Slug, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *PG) MemberRole(ctx context.Context, orgID, userID string) (string, error) {
	if _, err := uuid.Parse(orgID); err != nil {
		return "", ErrNotFound
	}
	if _, err := uuid.Parse(userID); err != nil {
		return "", ErrNotFound
	}
	var role string
	err := s.pool.QueryRow(ctx, `SELECT role FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return role, err
}

func (s *PG) DeleteOrg(ctx context.Context, orgID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) CreateInvite(ctx context.Context, orgID, email, role, tokenHash, invitedBy string, expiresAt time.Time) (*Invite, error) {
	inv := &Invite{ID: uuid.NewString(), OrgID: orgID, Email: email, Role: role, InvitedBy: invitedBy, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	// An open invite for the same email is replaced (resend) instead of piling up.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE org_invites SET revoked_at=NOW() WHERE org_id=$1 AND email=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, orgID, email); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO org_invites (id, org_id, email, role, token_hash, invited_by, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
	`, inv.ID, inv.OrgID, inv.Email, inv.Role, tokenHash, invitedBy, inv.ExpiresAt, inv.CreatedAt); err != nil {
		return nil, err
	}
	return inv, tx.Commit(ctx)
}

func (s *PG) CountPendingInvites(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM org_invites WHERE org_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()`, orgID).Scan(&n)
	return n, err
}

func (s *PG) ListInvites(ctx context.Context, orgID string) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, email, role, COALESCE(invited_by::text,''), expires_at, created_at
		FROM org_invites WHERE org_id=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC LIMIT 500
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.OrgID, &i.Email, &i.Role, &i.InvitedBy, &i.ExpiresAt, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *PG) RevokeInvite(ctx context.Context, orgID, inviteID string) error {
	if _, err := uuid.Parse(inviteID); err != nil {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `UPDATE org_invites SET revoked_at=NOW() WHERE id=$1 AND org_id=$2 AND accepted_at IS NULL AND revoked_at IS NULL`, inviteID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CheckInvite reports whether tokenHash is a live invite issued for email (does not consume it).
func (s *PG) CheckInvite(ctx context.Context, tokenHash, email string) (*Invite, error) {
	var inv Invite
	var storedHash string
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, email, role, token_hash, expires_at
		FROM org_invites WHERE token_hash=$1 AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > NOW()
	`, tokenHash).Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &storedHash, &inv.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(tokenHash)) != 1 || !strings.EqualFold(inv.Email, email) {
		return nil, ErrNotFound
	}
	return &inv, nil
}

// AcceptInvite consumes an invite for a user whose verified email matches the invite. The row is
// locked, so concurrent accepts of the same token yield exactly one success.
func (s *PG) AcceptInvite(ctx context.Context, tokenHash, userID, userEmail string) (*Org, string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(ctx)

	var inv Invite
	var storedHash string
	var accepted, revoked *time.Time
	err = tx.QueryRow(ctx, `
		SELECT id, org_id, email, role, token_hash, expires_at, accepted_at, revoked_at
		FROM org_invites WHERE token_hash=$1 FOR UPDATE
	`, tokenHash).Scan(&inv.ID, &inv.OrgID, &inv.Email, &inv.Role, &storedHash, &inv.ExpiresAt, &accepted, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	if subtle.ConstantTimeCompare([]byte(storedHash), []byte(tokenHash)) != 1 {
		return nil, "", ErrNotFound
	}
	if revoked != nil || time.Now().After(inv.ExpiresAt) {
		return nil, "", ErrNotFound
	}
	if accepted != nil {
		return nil, "", ErrAlreadyExists
	}
	if userEmail == "" || !strings.EqualFold(inv.Email, userEmail) {
		return nil, "", ErrForbidden
	}
	tag, err := tx.Exec(ctx, `UPDATE org_invites SET accepted_at=NOW() WHERE id=$1 AND accepted_at IS NULL`, inv.ID)
	if err != nil {
		return nil, "", err
	}
	if tag.RowsAffected() == 0 {
		return nil, "", ErrAlreadyExists
	}
	// Never downgrade an existing member.
	if _, err = tx.Exec(ctx, `
		INSERT INTO org_members (org_id, user_id, role, joined_at) VALUES ($1,$2,$3,NOW())
		ON CONFLICT (org_id, user_id) DO NOTHING
	`, inv.OrgID, userID, inv.Role); err != nil {
		return nil, "", err
	}
	org := &Org{}
	err = tx.QueryRow(ctx, `
		SELECT o.id, o.name, o.slug, o.created_at, m.role
		FROM organizations o JOIN org_members m ON m.org_id=o.id AND m.user_id=$2 WHERE o.id=$1
	`, inv.OrgID, userID).Scan(&org.ID, &org.Name, &org.Slug, &org.CreatedAt, &org.Role)
	if err != nil {
		return nil, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, "", err
	}
	return org, org.Role, nil
}

func (s *PG) ListMembers(ctx context.Context, orgID string) ([]Member, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT user_id, role, managed, joined_at FROM org_members WHERE org_id=$1 ORDER BY joined_at LIMIT 1000
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Role, &m.Managed, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PG) CountMembers(ctx context.Context, orgID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM org_members WHERE org_id=$1`, orgID).Scan(&n)
	return n, err
}

// lockedOwnerChange runs fn inside a transaction holding a lock on the org row, so two
// concurrent demotions/removals of different owners cannot both pass the owner-count check.
func (s *PG) lockedOwnerChange(ctx context.Context, orgID, userID string, fn func(tx pgx.Tx, currentRole string) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lock string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM organizations WHERE id=$1 FOR UPDATE`, orgID).Scan(&lock); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if err := fn(tx, role); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func ownersOtherThan(ctx context.Context, tx pgx.Tx, orgID, userID string) (int, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM org_members WHERE org_id=$1 AND role='owner' AND user_id<>$2`, orgID, userID).Scan(&n)
	return n, err
}

func (s *PG) SetMemberRole(ctx context.Context, orgID, userID, role string) error {
	return s.lockedOwnerChange(ctx, orgID, userID, func(tx pgx.Tx, current string) error {
		if current == "owner" && role != "owner" {
			n, err := ownersOtherThan(ctx, tx, orgID, userID)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastOwner
			}
		}
		_, err := tx.Exec(ctx, `UPDATE org_members SET role=$3 WHERE org_id=$1 AND user_id=$2`, orgID, userID, role)
		return err
	})
}

func (s *PG) RemoveMember(ctx context.Context, orgID, userID string) error {
	return s.lockedOwnerChange(ctx, orgID, userID, func(tx pgx.Tx, current string) error {
		if current == "owner" {
			n, err := ownersOtherThan(ctx, tx, orgID, userID)
			if err != nil {
				return err
			}
			if n == 0 {
				return ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM project_grants WHERE org_id=$1 AND user_id=$2`, orgID, userID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, userID)
		return err
	})
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint"))
}
