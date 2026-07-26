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

type Installation struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	InstallationID string    `json:"installation_id"`
	AccountLogin   string    `json:"account_login"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

type Repo struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	ProjectID      string    `json:"project_id"`
	InstallationID *string   `json:"installation_id,omitempty"`
	Provider       string    `json:"provider"`
	FullName       string    `json:"full_name"`
	CloneURL       string    `json:"clone_url"`
	DefaultBranch  string    `json:"default_branch"`
	CreatedAt      time.Time `json:"created_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateInstallation(ctx context.Context, orgID, installationID, login string) (*Installation, error) {
	inst := &Installation{
		ID: uuid.NewString(), OrgID: orgID, InstallationID: installationID,
		AccountLogin: login, Status: "active", CreatedAt: time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO github_installations (id, org_id, installation_id, account_login, status, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
	`, inst.ID, inst.OrgID, inst.InstallationID, inst.AccountLogin, inst.Status, inst.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return inst, nil
}

func (s *Store) ListInstallations(ctx context.Context, orgID string) ([]Installation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, installation_id, account_login, status, created_at
		FROM github_installations WHERE org_id=$1 ORDER BY created_at DESC
	`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Installation
	for rows.Next() {
		var i Installation
		if err := rows.Scan(&i.ID, &i.OrgID, &i.InstallationID, &i.AccountLogin, &i.Status, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if out == nil {
		out = []Installation{}
	}
	return out, rows.Err()
}

func (s *Store) ConnectRepo(ctx context.Context, orgID, projectID, installationID, fullName, cloneURL, branch, secret string) (*Repo, error) {
	r := &Repo{
		ID: uuid.NewString(), OrgID: orgID, ProjectID: projectID,
		Provider: "github", FullName: fullName, CloneURL: cloneURL,
		DefaultBranch: branch, CreatedAt: time.Now().UTC(),
	}
	var inst *string
	if installationID != "" {
		inst = &installationID
		r.InstallationID = inst
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO connected_repos (id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, webhook_secret, created_at)
		VALUES ($1,$2,$3,$4,'github',$5,$6,$7,$8,$9)
	`, r.ID, orgID, projectID, inst, fullName, cloneURL, branch, secret, r.CreatedAt)
	if err != nil {
		if isUnique(err) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return r, nil
}

func (s *Store) ListRepos(ctx context.Context, orgID, projectID string) ([]Repo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, created_at
		FROM connected_repos WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC
	`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.OrgID, &r.ProjectID, &r.InstallationID, &r.Provider, &r.FullName, &r.CloneURL, &r.DefaultBranch, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Repo{}
	}
	return out, rows.Err()
}

func (s *Store) GetRepo(ctx context.Context, orgID, projectID, repoID string) (*Repo, error) {
	r := &Repo{}
	err := s.pool.QueryRow(ctx, `
		SELECT id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, created_at
		FROM connected_repos WHERE id=$1 AND org_id=$2 AND project_id=$3
	`, repoID, orgID, projectID).Scan(&r.ID, &r.OrgID, &r.ProjectID, &r.InstallationID, &r.Provider, &r.FullName, &r.CloneURL, &r.DefaultBranch, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) DeleteRepo(ctx context.Context, orgID, projectID, repoID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM connected_repos WHERE id=$1 AND org_id=$2 AND project_id=$3`, repoID, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FindReposByFullName(ctx context.Context, fullName string) ([]Repo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, created_at
		FROM connected_repos WHERE full_name=$1
	`, fullName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.OrgID, &r.ProjectID, &r.InstallationID, &r.Provider, &r.FullName, &r.CloneURL, &r.DefaultBranch, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Repo{}
	}
	return out, rows.Err()
}

func (s *Store) GetWebhookSecret(ctx context.Context, repoID string) (string, error) {
	var secret string
	err := s.pool.QueryRow(ctx, `SELECT webhook_secret FROM connected_repos WHERE id=$1`, repoID).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return secret, err
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
