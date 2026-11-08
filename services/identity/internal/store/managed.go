package store

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
)

// GetUserByLogin resolves a username or an e-mail (a login containing '@' is an e-mail).
func (s *PG) GetUserByLogin(ctx context.Context, login string) (*User, error) {
	login = strings.ToLower(strings.TrimSpace(login))
	if strings.Contains(login, "@") {
		return s.GetUserByEmail(ctx, login)
	}
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE lower(username)=$1`, login))
}

func (s *PG) CreateManagedUser(ctx context.Context, orgID, name, username, email, passwordHash string, mustChange bool) (*User, error) {
	u := &User{ID: uuid.NewString(), Name: name, Username: username, Email: email, PasswordHash: passwordHash,
		MustChangePassword: mustChange, ManagedOrgID: &orgID, CreatedAt: time.Now().UTC()}
	var em any
	if email != "" {
		em = email
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO users (id, email, name, username, password_hash, managed_org_id, must_change_password, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$8)
	`, u.ID, em, u.Name, u.Username, u.PasswordHash, orgID, mustChange, u.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return u, nil
}

func (s *PG) GetManaged(ctx context.Context, orgID, userID string) (*User, error) {
	if _, err := uuid.Parse(userID); err != nil {
		return nil, ErrNotFound
	}
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id=$1 AND managed_org_id=$2`, userID, orgID))
}

func (s *PG) UpdateManaged(ctx context.Context, orgID, userID string, name *string, disabled *bool) (*User, error) {
	if _, err := s.GetManaged(ctx, orgID, userID); err != nil {
		return nil, err
	}
	if name != nil {
		if _, err := s.pool.Exec(ctx, `UPDATE users SET name=$3, updated_at=NOW() WHERE id=$1 AND managed_org_id=$2`, userID, orgID, *name); err != nil {
			return nil, err
		}
	}
	if disabled != nil {
		q := `UPDATE users SET disabled_at=NULL, updated_at=NOW() WHERE id=$1 AND managed_org_id=$2`
		if *disabled {
			q = `UPDATE users SET disabled_at=COALESCE(disabled_at, NOW()), updated_at=NOW() WHERE id=$1 AND managed_org_id=$2`
		}
		if _, err := s.pool.Exec(ctx, q, userID, orgID); err != nil {
			return nil, err
		}
	}
	return s.GetManaged(ctx, orgID, userID)
}

func (s *PG) ResetManagedPassword(ctx context.Context, orgID, userID, hash string, mustChange bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash=$3, must_change_password=$4, password_changed_at=NOW(), updated_at=NOW() WHERE id=$1 AND managed_org_id=$2`,
		userID, orgID, hash, mustChange)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) DeleteManaged(ctx context.Context, orgID, userID string) error {
	if _, err := uuid.Parse(userID); err != nil {
		return ErrNotFound
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id=$1 AND managed_org_id=$2`, userID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PG) TouchLogin(ctx context.Context, userID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE users SET last_login_at=NOW() WHERE id=$1`, userID)
	return err
}
