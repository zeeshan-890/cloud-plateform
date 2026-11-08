package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/repository/internal/cd"
	"github.com/jp-cloud/repository/internal/githubapp"
	"github.com/jp-cloud/repository/internal/store"
	"github.com/redis/go-redis/v9"
)

const installStateTTL = 10 * time.Minute

var (
	fullNameRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,99})/[A-Za-z0-9._-]{1,100}$`)
	branchRe   = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

// Repo is the persistence used by the handlers (implemented by *store.Store).
type Repo interface {
	ListInstallations(ctx context.Context, orgID string) ([]store.Installation, error)
	HasInstallation(ctx context.Context, orgID, installationID string) (bool, error)
	UpsertInstallation(ctx context.Context, orgID, installationID, login string) (*store.Installation, error)
	LatestInstallation(ctx context.Context, orgID string) (*store.Installation, error)
	ConnectRepo(ctx context.Context, p store.ConnectParams) (*store.Repo, error)
	ListRepos(ctx context.Context, orgID, projectID string) ([]store.Repo, error)
	GetRepo(ctx context.Context, orgID, projectID, id string) (*store.Repo, error)
	DeleteRepo(ctx context.Context, orgID, projectID, id string) error
	UpdateCD(ctx context.Context, orgID, projectID, id string, s cd.Settings) (*store.Repo, error)
	SetWebhookSecret(ctx context.Context, orgID, projectID, id, secret string) error
	WebhookSecretOf(ctx context.Context, orgID, projectID, id string) (string, error)
	FindForWebhook(ctx context.Context, fullName string) ([]store.WebhookRepo, error)
	Credentials(ctx context.Context, repoID string) (*store.Repo, string, error)
	InstallationForRepo(ctx context.Context, fullName string) (string, error)
	DeleteProject(ctx context.Context, projectID string) (int64, error)
	GetDeployKey(ctx context.Context, projectID string) (*store.DeployKey, error)
	GetOrCreateDeployKey(ctx context.Context, orgID, projectID string) (*store.DeployKey, error)
	RotateDeployKey(ctx context.Context, orgID, projectID string) (*store.DeployKey, error)
	DeployKeyPrivate(ctx context.Context, projectID string) (string, error)
}

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	Require(w http.ResponseWriter, r *http.Request, orgID, userID string, min membership.Role) (membership.Role, bool)
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store         Repo
	JWT           *jwtutil.Manager
	Authz         Authorizer
	DeploymentURL string
	// GlobalWebhookSecret verifies GitHub App level webhooks (optional, no default).
	GlobalWebhookSecret string
	PublicBaseURL       string
	DashboardURL        string
	GitHubAPI           string       // default https://api.github.com
	HTTP                *http.Client // outbound to GitHub (not internal)
	Internal            *http.Client // service-to-service (internalauth.NewClient)
	Redis               *redis.Client
	GitHub              *githubapp.Client
	// StubMode is GITHUB_MODE=stub: only effective when no GitHub App is configured.
	StubMode bool
	Log      *slog.Logger
}

func (h *Handler) log() *slog.Logger {
	if h.Log == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return h.Log
}

func (h *Handler) api() string {
	if h.GitHubAPI != "" {
		return strings.TrimRight(h.GitHubAPI, "/")
	}
	return "https://api.github.com"
}

func (h *Handler) appConfigured() bool { return h.GitHub != nil && h.GitHub.Configured() }
func (h *Handler) stub() bool          { return h.StubMode && !h.appConfigured() }

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	u := func(f http.HandlerFunc) http.Handler { return auth(f) }
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }

	mux.Handle("POST /orgs/{orgId}/github/install/start", u(h.InstallStart))
	mux.Handle("POST /orgs/{orgId}/github/install/callback", u(h.InstallCallback))
	mux.Handle("GET /orgs/{orgId}/github/installations", u(h.ListInstallations))
	mux.Handle("GET /orgs/{orgId}/github/repos", u(h.ListAvailableRepos))

	const p = "/orgs/{orgId}/projects/{projectId}"
	mux.Handle("POST "+p+"/repos", u(h.ConnectRepo))
	mux.Handle("GET "+p+"/repos", u(h.ListRepos))
	mux.Handle("DELETE "+p+"/repos/{repoId}", u(h.DisconnectRepo))
	mux.Handle("PATCH "+p+"/repos/{repoId}", u(h.UpdateRepoSettings))
	mux.Handle("POST "+p+"/repos/{repoId}/webhook/rotate", u(h.RotateWebhookSecret))
	mux.Handle("POST "+p+"/repos/{repoId}/webhook/install", u(h.InstallWebhook))
	mux.Handle("GET "+p+"/repos/{repoId}/folders", u(h.RepoFolders))
	mux.Handle("GET "+p+"/deploy-key", u(h.GetDeployKey))
	mux.Handle("POST "+p+"/deploy-key/generate", u(h.RotateDeployKey))

	// public: GitHub (signature verified per connection) and the App setup redirect (state verified)
	mux.HandleFunc("POST /webhooks/github", h.GitHubWebhook)
	mux.HandleFunc("GET /github/setup", h.GitHubSetup)
	mux.HandleFunc("GET /api/v1/github/setup", h.GitHubSetup)

	mux.Handle("GET /internal/orgs/{orgId}/projects/{projectId}/repos", in(h.InternalListRepos))
	mux.Handle("GET /internal/repos/{repoId}/clone-info", in(h.InternalCloneInfo))
	mux.Handle("GET /internal/projects/{projectId}/deploy-key", in(h.InternalDeployKey))
	mux.Handle("POST /internal/github/commit-status", in(h.InternalCommitStatus))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

// ---------------------------------------------------------------- auth helpers

func (h *Handler) orgAuth(w http.ResponseWriter, r *http.Request, min membership.Role) (orgID, uid string, ok bool) {
	orgID = r.PathValue("orgId")
	if _, err := uuid.Parse(orgID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid organization id")
		return
	}
	if min != membership.Viewer && readOnly(r) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
		return
	}
	uid = middleware.UserIDFrom(r.Context())
	if _, ok = h.Authz.Require(w, r, orgID, uid, min); !ok {
		return
	}
	return orgID, uid, true
}

func readOnly(r *http.Request) bool {
	c, ok := middleware.ClaimsFrom(r.Context())
	return ok && c.ReadOnly()
}

func (h *Handler) projAuth(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID, uid string, acc *membership.Access, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	for _, id := range []string{orgID, projectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
	}
	if min != membership.LevelView && readOnly(r) {
		httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
		return
	}
	uid = middleware.UserIDFrom(r.Context())
	if acc, ok = h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaRepository, min); !ok {
		return
	}
	return orgID, projectID, uid, acc, true
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------------------------------------------------------- installation flow

func (h *Handler) saveState(ctx context.Context, state, orgID, userID string) error {
	if h.Redis == nil {
		return errors.New("state store unavailable")
	}
	payload, _ := json.Marshal(map[string]string{"org_id": orgID, "user_id": userID})
	return h.Redis.Set(ctx, "jp:gh:install:"+state, payload, installStateTTL).Err()
}

// takeState atomically loads AND deletes the state (single use).
func (h *Handler) takeState(ctx context.Context, state string) (orgID, userID string, ok bool) {
	if h.Redis == nil || len(state) < 32 || len(state) > 128 {
		return "", "", false
	}
	raw, err := h.Redis.GetDel(ctx, "jp:gh:install:"+state).Bytes()
	if err != nil {
		return "", "", false
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) != nil || m["org_id"] == "" {
		return "", "", false
	}
	return m["org_id"], m["user_id"], true
}

func (h *Handler) InstallStart(w http.ResponseWriter, r *http.Request) {
	orgID, uid, ok := h.orgAuth(w, r, membership.Admin)
	if !ok {
		return
	}
	if !h.appConfigured() && !h.stub() {
		httpx.Error(w, http.StatusNotImplemented, "github_not_configured", "the GitHub App is not configured on this platform (GITHUB_APP_ID / key); connect repositories with a token or deploy key instead")
		return
	}
	state := randHex(24)
	if err := h.saveState(r.Context(), state, orgID, uid); err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "unavailable", "cannot store install state")
		return
	}
	if h.appConfigured() {
		httpx.JSON(w, http.StatusOK, map[string]any{"install_url": h.GitHub.InstallURL(state), "state": state, "mode": "github_app"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"install_url": fmt.Sprintf("%s/api/v1/orgs/%s/github/install/callback", strings.TrimRight(h.PublicBaseURL, "/"), orgID),
		"state":       state, "mode": "stub", "message": "GITHUB_MODE=stub: no real GitHub access (development only)"})
}

// InstallCallback completes an installation. The state is mandatory, single-use and bound
// to this org AND this user; the installation must exist for our App (checked with the
// App JWT) and must not belong to another organization.
func (h *Handler) InstallCallback(w http.ResponseWriter, r *http.Request) {
	orgID, uid, ok := h.orgAuth(w, r, membership.Admin)
	if !ok {
		return
	}
	var req struct {
		InstallationID string `json:"installation_id"`
		State          string `json:"state"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	stOrg, stUser, valid := h.takeState(r.Context(), req.State)
	if !valid || stOrg != orgID || stUser != uid {
		httpx.Error(w, http.StatusBadRequest, "invalid_state", "install state is missing, expired, already used or belongs to another session")
		return
	}
	inst, status, msg := h.completeInstall(r.Context(), orgID, req.InstallationID)
	if inst == nil {
		httpx.Error(w, status, "install_failed", msg)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"installation": inst})
}

func (h *Handler) completeInstall(ctx context.Context, orgID, installationID string) (*store.Installation, int, string) {
	login := ""
	switch {
	case h.appConfigured():
		if !githubapp.ValidInstallationID(installationID) {
			return nil, http.StatusBadRequest, "invalid installation_id"
		}
		l, err := h.GitHub.GetInstallationAccount(ctx, installationID)
		if err != nil {
			return nil, http.StatusBadRequest, "installation could not be verified with the GitHub App"
		}
		login = l
	case h.stub():
		installationID, login = "stub-"+randHex(4), "stub-github-org"
	default:
		return nil, http.StatusNotImplemented, "GitHub App not configured"
	}
	inst, err := h.Store.UpsertInstallation(ctx, orgID, installationID, login)
	if errors.Is(err, store.ErrInstallationOwned) {
		return nil, http.StatusConflict, "this installation is already linked to another organization"
	}
	if err != nil {
		return nil, http.StatusInternalServerError, "failed to save installation"
	}
	return inst, 0, ""
}

// GitHubSetup is the App's setup_url redirect (browser, no JWT). The state ties it to the
// org+user that started the flow; the installation is verified before saving.
func (h *Handler) GitHubSetup(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	dash := strings.TrimRight(h.DashboardURL, "/")
	if dash == "" {
		dash = "/"
	}
	fail := func(code string) { http.Redirect(w, r, dash+"/git?error="+url.QueryEscape(code), http.StatusFound) }
	orgID, _, ok := h.takeState(r.Context(), strings.TrimSpace(q.Get("state")))
	if !ok {
		fail("invalid_state")
		return
	}
	if inst, _, _ := h.completeInstall(r.Context(), orgID, strings.TrimSpace(q.Get("installation_id"))); inst == nil {
		fail("install_failed")
		return
	}
	http.Redirect(w, r, dash+"/git?installed=1", http.StatusFound)
}

func (h *Handler) ListInstallations(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := h.orgAuth(w, r, membership.Viewer)
	if !ok {
		return
	}
	list, err := h.Store.ListInstallations(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list installations")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"installations": list})
}

// ListAvailableRepos lists the repositories of the org's own GitHub App installation.
func (h *Handler) ListAvailableRepos(w http.ResponseWriter, r *http.Request) {
	orgID, _, ok := h.orgAuth(w, r, membership.Admin)
	if !ok {
		return
	}
	if h.appConfigured() {
		if inst, err := h.Store.LatestInstallation(r.Context(), orgID); err == nil {
			repos, err := h.GitHub.ListInstallationRepos(r.Context(), inst.InstallationID)
			if err != nil {
				httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
				return
			}
			out := make([]map[string]any, 0, len(repos))
			for _, repo := range repos {
				out = append(out, map[string]any{"full_name": repo.FullName, "default_branch": repo.DefaultBranch, "private": repo.Private})
			}
			httpx.JSON(w, http.StatusOK, map[string]any{"repos": out, "mode": "github_app"})
			return
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repos": []map[string]any{}, "mode": "direct"})
}

// ---------------------------------------------------------------- connect / disconnect

// parseFullName accepts owner/repo, https://github.com/owner/repo(.git) and git@github.com:owner/repo(.git).
func parseFullName(in string) (string, error) {
	in = strings.TrimSpace(in)
	switch {
	case strings.HasPrefix(in, "git@github.com:"):
		in = strings.TrimPrefix(in, "git@github.com:")
	case strings.HasPrefix(in, "ssh://git@github.com/"):
		in = strings.TrimPrefix(in, "ssh://git@github.com/")
	case strings.HasPrefix(in, "http://") || strings.HasPrefix(in, "https://"):
		u, err := url.Parse(in)
		if err != nil || u.User != nil || !strings.EqualFold(u.Hostname(), "github.com") {
			return "", errors.New("only github.com repositories are supported")
		}
		in = strings.Trim(u.Path, "/")
	}
	in = strings.TrimSuffix(strings.Trim(in, "/"), ".git")
	if !fullNameRe.MatchString(in) || strings.Contains(in, "..") {
		return "", errors.New("full_name must be owner/repo")
	}
	return in, nil
}

type ghRepoInfo struct {
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}

// githubRepo fetches repository metadata, optionally with a token.
func (h *Handler) githubRepo(ctx context.Context, fullName, token string) (*ghRepoInfo, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.api()+"/repos/"+fullName, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := h.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, resp.StatusCode, nil
	}
	var info ghRepoInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return nil, resp.StatusCode, err
	}
	return &info, resp.StatusCode, nil
}

// ConnectRepo (admin+) connects a GitHub repository. The clone URL is always derived from
// the verified full_name; tokens are stored encrypted and never echoed.
func (h *Handler) ConnectRepo(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		FullName       string `json:"full_name"`
		DefaultBranch  string `json:"default_branch"`
		InstallationID string `json:"installation_id"`
		Token          string `json:"token"`
		AuthType       string `json:"auth_type"`
		AllowForks     bool   `json:"allow_forks"`
		CloneURL       string `json:"clone_url"` // ignored: derived server-side
		cdRequest
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	settings, err := req.cdRequest.apply(cd.Defaults())
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	full, err := parseFullName(firstNonEmpty(req.FullName, req.CloneURL))
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if strings.HasPrefix(strings.TrimSpace(req.FullName), "git@") && req.AuthType == "" {
		req.AuthType = "deploy_key"
	}
	if req.AuthType == "" {
		switch {
		case req.Token != "":
			req.AuthType = "token"
		case req.InstallationID != "" || h.appConfigured():
			req.AuthType = "app"
		default:
			req.AuthType = "none"
		}
	}
	if req.DefaultBranch != "" && (!branchRe.MatchString(req.DefaultBranch) || strings.HasPrefix(req.DefaultBranch, "-") || strings.Contains(req.DefaultBranch, "..")) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid default_branch")
		return
	}
	p := store.ConnectParams{OrgID: orgID, ProjectID: projectID, FullName: full, AuthType: req.AuthType, Branch: req.DefaultBranch,
		CloneURL: "https://github.com/" + full + ".git", AllowForks: req.AllowForks, WebhookSecret: randHex(32), CD: settings}

	switch req.AuthType {
	case "app":
		if !h.appConfigured() {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "GitHub App is not configured")
			return
		}
		inst := req.InstallationID
		if inst == "" {
			li, err := h.Store.LatestInstallation(r.Context(), orgID)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "validation_error", "install the GitHub App for this organization first")
				return
			}
			inst = li.InstallationID
		}
		owned, err := h.Store.HasInstallation(r.Context(), orgID, inst)
		if err != nil || !owned {
			httpx.Error(w, http.StatusForbidden, "forbidden", "installation does not belong to this organization")
			return
		}
		repos, err := h.GitHub.ListInstallationRepos(r.Context(), inst)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
			return
		}
		found := false
		for _, rp := range repos {
			if strings.EqualFold(rp.FullName, full) {
				found = true
				p.FullName = rp.FullName
				if p.Branch == "" {
					p.Branch = rp.DefaultBranch
				}
			}
		}
		if !found {
			httpx.Error(w, http.StatusForbidden, "forbidden", "the GitHub App installation cannot access this repository")
			return
		}
		p.InstallationID = inst
	case "token":
		if req.Token == "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "token required")
			return
		}
		info, code, err := h.githubRepo(r.Context(), full, req.Token)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
			return
		}
		if info == nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", fmt.Sprintf("the token cannot access %s (GitHub answered %d)", full, code))
			return
		}
		p.Token = req.Token
		if p.Branch == "" {
			p.Branch = info.DefaultBranch
		}
	case "deploy_key":
		p.CloneURL = "git@github.com:" + full + ".git"
		if _, err := h.Store.GetOrCreateDeployKey(r.Context(), orgID, projectID); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to prepare deploy key")
			return
		}
	case "none":
		info, _, err := h.githubRepo(r.Context(), full, "")
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub did not answer")
			return
		}
		if info == nil || info.Private {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "repository not found or private: provide a token, deploy key or GitHub App")
			return
		}
		if p.Branch == "" {
			p.Branch = info.DefaultBranch
		}
	default:
		httpx.Error(w, http.StatusBadRequest, "validation_error", "auth_type must be app, token, deploy_key or none")
		return
	}
	if p.Branch == "" {
		p.Branch = "main"
	}
	repo, err := h.Store.ConnectRepo(r.Context(), p)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "this repository is already connected to this project")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to connect repo")
		return
	}
	h.emit(r.Context(), events.TopicDeploy, events.TypeRepoConnected, middleware.UserIDFrom(r.Context()), orgID, map[string]any{
		"repo_id": repo.ID, "project_id": projectID, "full_name": repo.FullName})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"repo":           repo,
		"webhook_secret": p.WebhookSecret, // shown exactly once
		"webhook_url":    h.webhookURLFor(repo.ID),
		"webhook_note":   "Add this secret to the GitHub webhook of the repository (content type application/json, events: push, pull_request, check_suite, workflow_run), or press Install webhook. It cannot be shown again.",
	})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func (h *Handler) ListRepos(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Store.ListRepos(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list repos")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repos": list})
}

func (h *Handler) DisconnectRepo(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if err := h.Store.DeleteRepo(r.Context(), orgID, projectID, id); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to disconnect repo")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- deploy keys

func keyJSON(k *store.DeployKey) map[string]any {
	return map[string]any{"deploy_key": map[string]any{"id": k.ID, "project_id": k.ProjectID, "public_key": k.PublicKey, "key_type": k.KeyType, "created_at": k.CreatedAt}}
}

// GetDeployKey returns the PUBLIC key (generated on first use by an admin).
func (h *Handler) GetDeployKey(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, acc, ok := h.projAuth(w, r, membership.LevelView)
	if !ok {
		return
	}
	k, err := h.Store.GetDeployKey(r.Context(), projectID)
	if errors.Is(err, store.ErrNotFound) {
		if !acc.Level(membership.AreaRepository).AtLeast(membership.LevelManage) {
			httpx.Error(w, http.StatusNotFound, "not_found", "no deploy key generated yet; ask an admin")
			return
		}
		k, err = h.Store.GetOrCreateDeployKey(r.Context(), orgID, projectID)
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to obtain deploy key")
		return
	}
	httpx.JSON(w, http.StatusOK, keyJSON(k))
}

func (h *Handler) RotateDeployKey(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, _, ok := h.projAuth(w, r, membership.LevelManage)
	if !ok {
		return
	}
	k, err := h.Store.RotateDeployKey(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to rotate deploy key")
		return
	}
	httpx.JSON(w, http.StatusOK, keyJSON(k))
}

// ---------------------------------------------------------------- internal

func (h *Handler) InternalListRepos(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListRepos(r.Context(), r.PathValue("orgId"), r.PathValue("projectId"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, rp := range list {
		out = append(out, map[string]any{"id": rp.ID, "full_name": rp.FullName, "default_branch": rp.DefaultBranch, "allow_forks": rp.AllowForks, "root_dir": rp.RootDir})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repos": out})
}

// InternalCloneInfo hands the worker what it needs to clone ONE connection: the clean URL
// plus a short-lived credential. Only the worker (internal token) can call it.
func (h *Handler) InternalCloneInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("repoId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	repo, token, err := h.Store.Credentials(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load credentials")
		return
	}
	if o := r.URL.Query().Get("org_id"); o != "" && o != repo.OrgID {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	if p := r.URL.Query().Get("project_id"); p != "" && p != repo.ProjectID {
		httpx.Error(w, http.StatusNotFound, "not_found", "repo not found")
		return
	}
	out := map[string]any{"clone_url": repo.CloneURL, "auth_type": repo.AuthType, "full_name": repo.FullName, "default_branch": repo.DefaultBranch, "root_dir": repo.RootDir}
	switch repo.AuthType {
	case "app":
		if repo.InstallationID == nil || !h.appConfigured() {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "GitHub App unavailable")
			return
		}
		t, err := h.GitHub.InstallationToken(r.Context(), *repo.InstallationID)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "cannot mint installation token")
			return
		}
		out["token"] = t
	case "token":
		out["token"] = token
	case "deploy_key":
		key, err := h.Store.DeployKeyPrivate(r.Context(), repo.ProjectID)
		if err != nil {
			httpx.Error(w, http.StatusBadGateway, "bad_gateway", "deploy key unavailable")
			return
		}
		out["private_key"] = key
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, out)
}

// InternalDeployKey is kept for the worker's older call path; internal token only.
func (h *Handler) InternalDeployKey(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	k, err := h.Store.GetDeployKey(r.Context(), pid)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "deploy key not found")
		return
	}
	priv, perr := h.Store.DeployKeyPrivate(r.Context(), pid)
	if err != nil || perr != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to fetch deploy key")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, map[string]any{"public_key": k.PublicKey, "private_key": priv, "key_type": k.KeyType})
}

func (h *Handler) InternalCommitStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FullName    string `json:"full_name"`
		SHA         string `json:"sha"`
		State       string `json:"state"`
		Description string `json:"description"`
		TargetURL   string `json:"target_url"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if !fullNameRe.MatchString(req.FullName) || !regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`).MatchString(req.SHA) {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "missing or invalid sha/repo"})
		return
	}
	if !h.appConfigured() {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "app_not_configured"})
		return
	}
	inst, err := h.Store.InstallationForRepo(r.Context(), req.FullName)
	if err != nil {
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": true, "skipped": true, "reason": "no_installation"})
		return
	}
	if req.Description == "" {
		req.Description = "jp deployment " + req.State
	}
	if err := h.GitHub.CreateCommitStatus(r.Context(), inst, req.FullName, req.SHA, req.State, req.Description, req.TargetURL); err != nil {
		h.log().Warn("commit status failed", "repo", req.FullName, "err", err)
		httpx.JSON(w, http.StatusOK, map[string]any{"ok": false, "error": "GitHub rejected the status"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	n, err := h.Store.DeleteProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"repos_removed": n})
}

func (h *Handler) emit(ctx context.Context, topic, typ, actorID, orgID string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	_, _ = events.PublishJSON(ctx, h.Redis, topic, events.New(topic, typ, actorID, orgID, payload))
}

func (h *Handler) internalPost(ctx context.Context, path string, body any) (int, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.DeploymentURL, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	hc := h.Internal
	if hc == nil {
		hc = internalauth.NewClient(20 * time.Second)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, nil
}
