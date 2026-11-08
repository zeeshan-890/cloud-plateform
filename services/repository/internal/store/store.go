package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jp-cloud/repository/internal/cd"
	"github.com/jp-cloud/repository/internal/secbox"
	"github.com/jp-cloud/repository/internal/sshkey"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	// ErrInstallationOwned: the GitHub installation is already linked to another organization.
	ErrInstallationOwned = errors.New("installation belongs to another organization")
)

// DeployKey never exposes the private key through JSON.
type DeployKey struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"org_id"`
	ProjectID string    `json:"project_id"`
	PublicKey string    `json:"public_key"`
	KeyType   string    `json:"key_type"`
	CreatedAt time.Time `json:"created_at"`
}

type Installation struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	InstallationID string    `json:"installation_id"`
	AccountLogin   string    `json:"account_login"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
}

// Repo is a connected repository. It carries no credentials.
type Repo struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	ProjectID      string    `json:"project_id"`
	InstallationID *string   `json:"installation_id,omitempty"`
	Provider       string    `json:"provider"`
	FullName       string    `json:"full_name"`
	CloneURL       string    `json:"clone_url"` // token-free
	DefaultBranch  string    `json:"default_branch"`
	AuthType       string    `json:"auth_type"` // app | token | deploy_key | none
	AllowForks     bool      `json:"allow_forks"`
	CreatedAt      time.Time `json:"created_at"`

	// continuous deployment (see package cd)
	RootDir        string   `json:"root_dir"`
	AutoDeploy     bool     `json:"auto_deploy"`
	DeployBranch   string   `json:"deploy_branch"`
	BranchPreviews bool     `json:"branch_previews"`
	PRPreviews     bool     `json:"pr_previews"`
	WatchPaths     []string `json:"watch_paths"`
	IgnorePaths    []string `json:"ignore_paths"`
	RequireCI      bool     `json:"require_ci"`
}

// CD returns the continuous-deployment settings of the connection.
func (r Repo) CD() cd.Settings {
	return cd.Settings{RootDir: r.RootDir, AutoDeploy: r.AutoDeploy, DeployBranch: r.DeployBranch, BranchPreviews: r.BranchPreviews,
		PRPreviews: r.PRPreviews, WatchPaths: r.WatchPaths, IgnorePaths: r.IgnorePaths, RequireCI: r.RequireCI}
}

func (r *Repo) applyCD(s cd.Settings) {
	r.RootDir, r.AutoDeploy, r.DeployBranch, r.BranchPreviews, r.PRPreviews, r.RequireCI = s.RootDir, s.AutoDeploy, s.DeployBranch, s.BranchPreviews, s.PRPreviews, s.RequireCI
	r.WatchPaths, r.IgnorePaths = nonNil(s.WatchPaths), nonNil(s.IgnorePaths)
}

func nonNil(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// dest lists the scan targets matching repoCols.
func (r *Repo) dest() []any {
	return []any{&r.ID, &r.OrgID, &r.ProjectID, &r.InstallationID, &r.Provider, &r.FullName, &r.CloneURL, &r.DefaultBranch, &r.AuthType, &r.AllowForks, &r.CreatedAt,
		&r.RootDir, &r.AutoDeploy, &r.DeployBranch, &r.BranchPreviews, &r.PRPreviews, &r.WatchPaths, &r.IgnorePaths, &r.RequireCI}
}

// WebhookRepo is a connection together with its decrypted webhook secret (internal use).
type WebhookRepo struct {
	Repo
	WebhookSecret string
}

// ConnectParams create a connection.
type ConnectParams struct {
	OrgID, ProjectID, InstallationID, FullName, CloneURL, Branch, AuthType string
	Token, WebhookSecret                                                   string
	AllowForks                                                             bool
	CD                                                                     cd.Settings
}

type Store struct {
	pool *pgxpool.Pool
	box  *secbox.Box
}

func New(pool *pgxpool.Pool, box *secbox.Box) *Store { return &Store{pool: pool, box: box} }

const repoCols = `id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, auth_type, allow_forks, created_at,
	root_dir, auto_deploy, deploy_branch, branch_previews, pr_previews, watch_paths, ignore_paths, require_ci`

func scanRepo(row pgx.Row) (*Repo, error) {
	r := &Repo{}
	err := row.Scan(r.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}

func (s *Store) repoRows(rows pgx.Rows) ([]Repo, error) {
	defer rows.Close()
	out := []Repo{}
	for rows.Next() {
		r, err := scanRepo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- installations

func (s *Store) ListInstallations(ctx context.Context, orgID string) ([]Installation, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, installation_id, account_login, status, created_at FROM github_installations WHERE org_id=$1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Installation{}
	for rows.Next() {
		var i Installation
		if err := rows.Scan(&i.ID, &i.OrgID, &i.InstallationID, &i.AccountLogin, &i.Status, &i.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// HasInstallation reports whether the org owns the installation.
func (s *Store) HasInstallation(ctx context.Context, orgID, installationID string) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM github_installations WHERE org_id=$1 AND installation_id=$2 AND status='active'`, orgID, installationID).Scan(&n)
	return n > 0, err
}

// UpsertInstallation links an installation to exactly one organization.
func (s *Store) UpsertInstallation(ctx context.Context, orgID, installationID, login string) (*Installation, error) {
	var owner string
	err := s.pool.QueryRow(ctx, `SELECT org_id::text FROM github_installations WHERE installation_id=$1 LIMIT 1`, installationID).Scan(&owner)
	if err == nil && owner != orgID {
		return nil, ErrInstallationOwned
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	i := &Installation{ID: uuid.NewString(), OrgID: orgID, InstallationID: installationID, AccountLogin: login, Status: "active", CreatedAt: time.Now().UTC()}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO github_installations (id, org_id, installation_id, account_login, status, created_at)
		VALUES ($1,$2,$3,$4,'active',$5)
		ON CONFLICT (org_id, installation_id) DO UPDATE SET account_login=EXCLUDED.account_login, status='active'
		RETURNING id, created_at`, i.ID, i.OrgID, i.InstallationID, i.AccountLogin, i.CreatedAt).Scan(&i.ID, &i.CreatedAt)
	return i, err
}

// LatestInstallation returns the newest active installation of the org.
func (s *Store) LatestInstallation(ctx context.Context, orgID string) (*Installation, error) {
	i := &Installation{}
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, installation_id, account_login, status, created_at FROM github_installations
		WHERE org_id=$1 AND status='active' ORDER BY created_at DESC LIMIT 1`, orgID).Scan(&i.ID, &i.OrgID, &i.InstallationID, &i.AccountLogin, &i.Status, &i.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return i, err
}

// ---------------------------------------------------------------- repos

func (s *Store) ConnectRepo(ctx context.Context, p ConnectParams) (*Repo, error) {
	tok, err := s.box.Seal(p.Token)
	if err != nil {
		return nil, err
	}
	sec, err := s.box.Seal(p.WebhookSecret)
	if err != nil {
		return nil, err
	}
	r := &Repo{ID: uuid.NewString(), OrgID: p.OrgID, ProjectID: p.ProjectID, Provider: "github", FullName: p.FullName, CloneURL: p.CloneURL,
		DefaultBranch: p.Branch, AuthType: p.AuthType, AllowForks: p.AllowForks, CreatedAt: time.Now().UTC()}
	r.applyCD(p.CD)
	var inst *string
	if p.InstallationID != "" {
		inst = &p.InstallationID
		r.InstallationID = inst
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO connected_repos (id, org_id, project_id, installation_id, provider, full_name, clone_url, default_branch, webhook_secret,
			auth_type, token_enc, webhook_secret_enc, allow_forks, legacy_scrubbed, created_at,
			root_dir, auto_deploy, deploy_branch, branch_previews, pr_previews, watch_paths, ignore_paths, require_ci)
		VALUES ($1,$2,$3,$4,'github',$5,$6,$7,'',$8,$9,$10,$11,TRUE,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
		r.ID, r.OrgID, r.ProjectID, inst, r.FullName, r.CloneURL, r.DefaultBranch, r.AuthType, tok, sec, r.AllowForks, r.CreatedAt,
		r.RootDir, r.AutoDeploy, r.DeployBranch, r.BranchPreviews, r.PRPreviews, r.WatchPaths, r.IgnorePaths, r.RequireCI)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint") {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return r, nil
}

func (s *Store) ListRepos(ctx context.Context, orgID, projectID string) ([]Repo, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+repoCols+` FROM connected_repos WHERE org_id=$1 AND project_id=$2 ORDER BY created_at DESC`, orgID, projectID)
	if err != nil {
		return nil, err
	}
	return s.repoRows(rows)
}

func (s *Store) GetRepo(ctx context.Context, orgID, projectID, id string) (*Repo, error) {
	return scanRepo(s.pool.QueryRow(ctx, `SELECT `+repoCols+` FROM connected_repos WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID))
}

func (s *Store) DeleteRepo(ctx context.Context, orgID, projectID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM connected_repos WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateCD replaces the continuous-deployment settings of one connection.
func (s *Store) UpdateCD(ctx context.Context, orgID, projectID, id string, c cd.Settings) (*Repo, error) {
	r := &Repo{}
	r.applyCD(c)
	return scanRepo(s.pool.QueryRow(ctx, `UPDATE connected_repos SET root_dir=$4, auto_deploy=$5, deploy_branch=$6, branch_previews=$7, pr_previews=$8,
		watch_paths=$9, ignore_paths=$10, require_ci=$11 WHERE id=$1 AND org_id=$2 AND project_id=$3 RETURNING `+repoCols,
		id, orgID, projectID, r.RootDir, r.AutoDeploy, r.DeployBranch, r.BranchPreviews, r.PRPreviews, r.WatchPaths, r.IgnorePaths, r.RequireCI))
}

// SetWebhookSecret replaces the (sealed) webhook secret of one connection.
func (s *Store) SetWebhookSecret(ctx context.Context, orgID, projectID, id, secret string) error {
	sec, err := s.box.Seal(secret)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE connected_repos SET webhook_secret_enc=$4 WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID, sec)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// WebhookSecretOf returns the decrypted webhook secret of one connection (to install the GitHub hook).
func (s *Store) WebhookSecretOf(ctx context.Context, orgID, projectID, id string) (string, error) {
	var enc []byte
	err := s.pool.QueryRow(ctx, `SELECT webhook_secret_enc FROM connected_repos WHERE id=$1 AND org_id=$2 AND project_id=$3`, id, orgID, projectID).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.box.Open(enc)
}

// FindForWebhook returns every connection of fullName with its decrypted webhook secret.
func (s *Store) FindForWebhook(ctx context.Context, fullName string) ([]WebhookRepo, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+repoCols+`, webhook_secret_enc FROM connected_repos WHERE lower(full_name)=lower($1)`, fullName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WebhookRepo
	for rows.Next() {
		var w WebhookRepo
		var enc []byte
		if err := rows.Scan(append(w.Repo.dest(), &enc)...); err != nil {
			return nil, err
		}
		if w.WebhookSecret, err = s.box.Open(enc); err != nil {
			w.WebhookSecret = "" // undecryptable => can never verify
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// Credentials returns the decrypted personal token of a connection.
func (s *Store) Credentials(ctx context.Context, repoID string) (*Repo, string, error) {
	var enc []byte
	r := &Repo{}
	err := s.pool.QueryRow(ctx, `SELECT `+repoCols+`, token_enc FROM connected_repos WHERE id=$1`, repoID).
		Scan(append(r.dest(), &enc)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	tok, err := s.box.Open(enc)
	return r, tok, err
}

// InstallationForRepo returns a GitHub installation id used by any connection of fullName.
func (s *Store) InstallationForRepo(ctx context.Context, fullName string) (string, error) {
	var inst *string
	err := s.pool.QueryRow(ctx, `SELECT installation_id FROM connected_repos WHERE lower(full_name)=lower($1) AND installation_id IS NOT NULL AND installation_id <> '' ORDER BY created_at DESC LIMIT 1`, fullName).Scan(&inst)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && (inst == nil || *inst == "")) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return *inst, nil
}

// DeleteProject removes connections and the deploy key of a project.
func (s *Store) DeleteProject(ctx context.Context, projectID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM connected_repos WHERE project_id=$1`, projectID)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx, `DELETE FROM project_deploy_keys WHERE project_id=$1`, projectID)
	return tag.RowsAffected(), err
}

// ---------------------------------------------------------------- deploy keys

func (s *Store) newKey(orgID, projectID string) (*DeployKey, []byte, error) {
	pub, priv, err := sshkey.GenerateEd25519(fmt.Sprintf("jp-deploy-%s", shortID(projectID)))
	if err != nil {
		return nil, nil, err
	}
	enc, err := s.box.Seal(priv)
	if err != nil {
		return nil, nil, err
	}
	return &DeployKey{ID: uuid.NewString(), OrgID: orgID, ProjectID: projectID, PublicKey: pub, KeyType: "ed25519", CreatedAt: time.Now().UTC()}, enc, nil
}

// GetDeployKey returns the public part, or ErrNotFound.
func (s *Store) GetDeployKey(ctx context.Context, projectID string) (*DeployKey, error) {
	k := &DeployKey{}
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, project_id, public_key, key_type, created_at FROM project_deploy_keys WHERE project_id=$1`, projectID).
		Scan(&k.ID, &k.OrgID, &k.ProjectID, &k.PublicKey, &k.KeyType, &k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return k, err
}

// GetOrCreateDeployKey returns the project's key, generating it on first use.
func (s *Store) GetOrCreateDeployKey(ctx context.Context, orgID, projectID string) (*DeployKey, error) {
	if k, err := s.GetDeployKey(ctx, projectID); err == nil {
		return k, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	k, enc, err := s.newKey(orgID, projectID)
	if err != nil {
		return nil, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO project_deploy_keys (id, org_id, project_id, public_key, private_key, private_key_enc, key_type, created_at)
		VALUES ($1,$2,$3,$4,'',$5,$6,$7) ON CONFLICT (project_id) DO NOTHING`, k.ID, k.OrgID, k.ProjectID, k.PublicKey, enc, k.KeyType, k.CreatedAt); err != nil {
		return nil, err
	}
	return s.GetDeployKey(ctx, projectID)
}

// RotateDeployKey replaces the project's key.
func (s *Store) RotateDeployKey(ctx context.Context, orgID, projectID string) (*DeployKey, error) {
	k, enc, err := s.newKey(orgID, projectID)
	if err != nil {
		return nil, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO project_deploy_keys (id, org_id, project_id, public_key, private_key, private_key_enc, key_type, created_at)
		VALUES ($1,$2,$3,$4,'',$5,$6,$7)
		ON CONFLICT (project_id) DO UPDATE SET public_key=EXCLUDED.public_key, private_key='', private_key_enc=EXCLUDED.private_key_enc, created_at=EXCLUDED.created_at`,
		k.ID, k.OrgID, k.ProjectID, k.PublicKey, enc, k.KeyType, k.CreatedAt)
	return k, err
}

// DeployKeyPrivate returns the decrypted private key (internal use only).
func (s *Store) DeployKeyPrivate(ctx context.Context, projectID string) (string, error) {
	var enc []byte
	err := s.pool.QueryRow(ctx, `SELECT private_key_enc FROM project_deploy_keys WHERE project_id=$1`, projectID).Scan(&enc)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.box.Open(enc)
}

// ---------------------------------------------------------------- legacy scrub

// ScrubLegacy moves credentials written by older versions (tokens inside clone URLs,
// plaintext deploy keys and webhook secrets) into encrypted columns. Idempotent.
func (s *Store) ScrubLegacy(ctx context.Context) (int, error) {
	n := 0
	rows, err := s.pool.Query(ctx, `SELECT id, clone_url, webhook_secret FROM connected_repos WHERE NOT legacy_scrubbed LIMIT 1000`)
	if err != nil {
		return 0, err
	}
	type row struct{ id, url, secret string }
	var list []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.url, &r.secret); err != nil {
			rows.Close()
			return 0, err
		}
		list = append(list, r)
	}
	rows.Close()
	for _, r := range list {
		clean, token := StripURLCredentials(r.url)
		authType := "none"
		switch {
		case token != "":
			authType = "token"
		case strings.HasPrefix(clean, "git@"):
			authType = "deploy_key"
		}
		tok, _ := s.box.Seal(token)
		sec := []byte(nil)
		if r.secret != "" && r.secret != "dev-webhook-secret" {
			sec, _ = s.box.Seal(r.secret)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE connected_repos SET clone_url=$2, auth_type=$3, token_enc=COALESCE($4, token_enc), webhook_secret_enc=COALESCE($5, webhook_secret_enc),
			webhook_secret='', legacy_scrubbed=TRUE WHERE id=$1`, r.id, clean, authType, nilIfEmpty(tok), nilIfEmpty(sec)); err != nil {
			return n, err
		}
		n++
	}
	krows, err := s.pool.Query(ctx, `SELECT project_id::text, private_key FROM project_deploy_keys WHERE private_key <> ''`)
	if err != nil {
		return n, err
	}
	type kr struct{ pid, key string }
	var keys []kr
	for krows.Next() {
		var k kr
		if err := krows.Scan(&k.pid, &k.key); err != nil {
			krows.Close()
			return n, err
		}
		keys = append(keys, k)
	}
	krows.Close()
	for _, k := range keys {
		enc, err := s.box.Seal(k.key)
		if err != nil {
			return n, err
		}
		if _, err := s.pool.Exec(ctx, `UPDATE project_deploy_keys SET private_key_enc=$2, private_key='' WHERE project_id=$1`, k.pid, enc); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func nilIfEmpty(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// StripURLCredentials removes user:password@ from a URL and returns the password part.
func StripURLCredentials(u string) (clean, secret string) {
	i := strings.Index(u, "://")
	if i < 0 {
		return u, ""
	}
	rest := u[i+3:]
	at := strings.Index(rest, "@")
	slash := strings.Index(rest, "/")
	if at < 0 || (slash >= 0 && at > slash) {
		return u, ""
	}
	userinfo := rest[:at]
	if c := strings.Index(userinfo, ":"); c >= 0 {
		secret = userinfo[c+1:]
	} else {
		secret = userinfo
	}
	return u[:i+3] + rest[at+1:], secret
}

func shortID(id string) string {
	clean := strings.ReplaceAll(id, "-", "")
	if len(clean) > 8 {
		return clean[:8]
	}
	if clean == "" {
		return "unknown"
	}
	return clean
}
