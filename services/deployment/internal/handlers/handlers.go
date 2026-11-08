package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/deployment/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	shaRe    = regexp.MustCompile(`^([0-9a-fA-F]{7,64}|HEAD)$`)
)

// Repo is the persistence used by the handlers (implemented by *store.Store).
type Repo interface {
	Create(ctx context.Context, d *store.Deployment) error
	List(ctx context.Context, orgID, projectID string) ([]store.Deployment, error)
	Get(ctx context.Context, orgID, projectID, id string) (*store.Deployment, error)
	GetByID(ctx context.Context, id string) (*store.Deployment, error)
	RollbackCandidates(ctx context.Context, orgID, projectID string, limit int) ([]store.Deployment, error)
	Apply(ctx context.Context, id string, p store.Patch) (*store.Deployment, bool, error)
	SupersedeOlder(ctx context.Context, d *store.Deployment) error
	ActivePreviews(ctx context.Context, orgID, projectID string, pr int, branch string) ([]store.Deployment, error)
	PreviewsByPR(ctx context.Context, orgID, projectID, fullName string, pr int) ([]store.Deployment, error)
	ExpiredPreviews(ctx context.Context, cutoff time.Time) ([]store.Deployment, error)
	DeleteProject(ctx context.Context, projectID string) (int64, error)
	LiveByProject(ctx context.Context, projectID string) ([]store.Deployment, error)
}

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store         Repo
	JWT           *jwtutil.Manager
	Authz         Authorizer
	BuildURL      string
	RepositoryURL string
	RuntimeURL    string
	DomainURL     string
	RegistryURL   string
	PublicBaseURL string
	HTTP          *http.Client // must be internalauth.NewClient(...)
	Log           *slog.Logger
	Redis         *redis.Client

	wg   sync.WaitGroup
	sem  chan struct{}
	once sync.Once
}

// Wait blocks until background notifications finished (graceful shutdown).
func (h *Handler) Wait() { h.wg.Wait() }

func (h *Handler) init() {
	h.once.Do(func() {
		h.sem = make(chan struct{}, 16)
		if h.Log == nil {
			h.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
	})
}

func (h *Handler) async(f func()) {
	h.init()
	select {
	case h.sem <- struct{}{}:
	default:
		h.Log.Warn("notification queue full; dropping")
		return
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer func() { <-h.sem }()
		f()
	}()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	h.init()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	u := func(f http.HandlerFunc) http.Handler { return auth(f) }
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }

	const p = "/orgs/{orgId}/projects/{projectId}/deployments"
	mux.Handle("POST "+p, u(h.Create))
	mux.Handle("GET "+p, u(h.List))
	mux.Handle("POST "+p+"/rollback", u(h.Rollback))
	mux.Handle("GET "+p+"/{deploymentId}", u(h.Get))
	mux.Handle("POST "+p+"/{deploymentId}/rollback", u(h.RollbackTo))
	mux.Handle("POST "+p+"/{deploymentId}/cancel", u(h.Cancel))

	mux.Handle("POST /internal/deployments/from-git", in(h.FromGit))
	mux.Handle("POST /internal/deployments/{deploymentId}/status", in(h.InternalUpdateStatus))
	mux.Handle("POST /internal/deployments/{deploymentId}/preview-url", in(h.InternalSetPreviewURL))
	mux.Handle("POST /internal/preview/teardown", in(h.InternalPreviewTeardown))
	mux.Handle("POST /internal/cleanup/preview-deploys", in(h.CleanupPreview))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID, uid string, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	for _, id := range []string{orgID, projectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
	}
	if min != membership.LevelView {
		if c, has := middleware.ClaimsFrom(r.Context()); has && c.ReadOnly() {
			httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
			return
		}
	}
	uid = middleware.UserIDFrom(r.Context())
	if _, ok = h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDeployments, min); !ok {
		return
	}
	return orgID, projectID, uid, true
}

func normalizeStrategy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "blue_green", "blue-green", "bluegreen":
		return "blue_green"
	default:
		return "rolling"
	}
}

// ---------------------------------------------------------------- outbound

func (h *Handler) call(ctx context.Context, method, u string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if out != nil && resp.StatusCode < 300 {
		_ = json.Unmarshal(b, out)
	}
	return resp.StatusCode, nil
}

func base(u string) string { return strings.TrimRight(u, "/") }

type repoInfo struct {
	ID            string `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

// repos lists the project's connected repositories (no URLs, no credentials).
func (h *Handler) repos(ctx context.Context, orgID, projectID string) ([]repoInfo, error) {
	var out struct {
		Repos []repoInfo `json:"repos"`
	}
	code, err := h.call(ctx, http.MethodGet, fmt.Sprintf("%s/internal/orgs/%s/projects/%s/repos", base(h.RepositoryURL), url.PathEscape(orgID), url.PathEscape(projectID)), nil, &out)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("repository service status %d", code)
	}
	return out.Repos, nil
}

func (h *Handler) enqueueBuild(ctx context.Context, d *store.Deployment) (string, error) {
	repoID := ""
	if d.RepoID != nil {
		repoID = *d.RepoID
	}
	var out struct {
		Build struct {
			ID string `json:"id"`
		} `json:"build"`
	}
	code, err := h.call(ctx, http.MethodPost, base(h.BuildURL)+"/internal/builds", map[string]any{
		"org_id": d.OrgID, "project_id": d.ProjectID, "deployment_id": d.ID, "repo_id": repoID,
		"git_sha": d.GitSHA, "git_branch": d.GitBranch, "pr_number": d.PRNumber, "full_name": d.FullName,
	}, &out)
	if err != nil {
		return "", err
	}
	if code >= 300 || out.Build.ID == "" {
		return "", fmt.Errorf("build service status %d", code)
	}
	return out.Build.ID, nil
}

// ---------------------------------------------------------------- state changes

// transition applies a patch, then runs the side effects of a real change exactly once.
func (h *Handler) transition(ctx context.Context, id string, p store.Patch) (*store.Deployment, error) {
	d, changed, err := h.Store.Apply(ctx, id, p)
	if err != nil {
		return d, err
	}
	if changed {
		h.afterChange(ctx, d)
	}
	return d, nil
}

func (h *Handler) afterChange(ctx context.Context, d *store.Deployment) {
	h.postCommitStatus(d)
	switch d.Status {
	case store.Deploying, store.Ready, store.Failed, store.Canceled:
		h.publishDeploy(ctx, d)
	}
	if d.Status == store.Ready && !d.IsPreview {
		if err := h.Store.SupersedeOlder(ctx, d); err != nil {
			h.Log.Warn("supersede older failed", "deployment", d.ID, "err", err)
		}
	}
}

func (h *Handler) startBuild(ctx context.Context, d *store.Deployment) {
	buildID, err := h.enqueueBuild(ctx, d)
	if err != nil {
		h.Log.Warn("enqueue build failed", "deployment", d.ID, "err", err)
		if nd, e := h.transition(ctx, d.ID, store.Patch{Status: store.Failed, Error: "failed to enqueue build"}); e == nil {
			*d = *nd
		}
		return
	}
	if nd, e := h.transition(ctx, d.ID, store.Patch{Status: store.Building, BuildID: buildID}); e == nil {
		*d = *nd
	}
}

// postCommitStatus reports the REAL state (pending until live, success only when ready,
// failure for failed/canceled) to GitHub through the repository service.
func (h *Handler) postCommitStatus(d *store.Deployment) {
	sha := strings.TrimSpace(d.GitSHA)
	if h.RepositoryURL == "" || sha == "" || sha == "HEAD" || d.FullName == "" || !shaRe.MatchString(sha) {
		return
	}
	target := strings.TrimRight(h.PublicBaseURL, "/")
	if d.PreviewURL != "" && d.Status == store.Ready {
		target = d.PreviewURL
	}
	state := store.CommitStateFor(d.Status)
	desc := map[string]string{store.Queued: "deployment queued", store.Building: "building", store.Deploying: "starting and health-checking",
		store.Ready: "deployment is live", store.Failed: "deployment failed", store.Canceled: "deployment canceled"}[d.Status]
	if desc == "" {
		return
	}
	body := map[string]any{"full_name": d.FullName, "sha": sha, "state": state, "description": "jp: " + desc, "target_url": target}
	h.async(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if code, err := h.call(ctx, http.MethodPost, base(h.RepositoryURL)+"/internal/github/commit-status", body, nil); err != nil || code >= 300 {
			h.Log.Warn("commit status failed", "deployment", d.ID, "err", err, "status", code)
		}
	})
}

// publishDeploy emits deploy.updated once per (deployment, status).
func (h *Handler) publishDeploy(ctx context.Context, d *store.Deployment) {
	if h.Redis == nil {
		return
	}
	key := "jp:deploy:evt:" + d.ID + ":" + d.Status
	ok, err := h.Redis.SetNX(ctx, key, "1", 24*time.Hour).Result()
	if err != nil || !ok {
		return
	}
	buildID := ""
	if d.BuildID != nil {
		buildID = *d.BuildID
	}
	strategy := normalizeStrategy(d.Strategy)
	if d.IsPreview {
		strategy = "rolling"
	}
	env := events.New(events.TopicDeploy, events.TypeDeployUpdated, "", d.OrgID, map[string]any{
		"deployment_id": d.ID, "org_id": d.OrgID, "project_id": d.ProjectID, "status": d.Status, "image_ref": d.ImageRef,
		"build_id": buildID, "source": d.Source, "git_sha": d.GitSHA, "git_branch": d.GitBranch, "pr_number": d.PRNumber,
		"message": d.Message, "preview": d.IsPreview, "fork": d.Fork, "preview_url": d.PreviewURL, "strategy": strategy,
		"spec": SpecFromConfig(d.JPConfig), "error": d.Error,
	})
	if _, err := events.PublishJSON(ctx, h.Redis, events.TopicDeploy, env); err != nil {
		_ = h.Redis.Del(ctx, key).Err() // allow a retry of the same transition
		h.Log.Warn("publish deploy event failed", "deployment", d.ID, "err", err)
	}
}

// ---------------------------------------------------------------- user API

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list deployments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployments": list})
}

func (h *Handler) load(w http.ResponseWriter, r *http.Request, orgID, projectID string) (*store.Deployment, bool) {
	id := r.PathValue("deploymentId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return nil, false
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return nil, false
	}
	return d, true
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	if d, ok := h.load(w, r, orgID, projectID); ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"deployment": d})
	}
}

// Create starts a deployment of the project's connected repository. Any clone_url or
// full_name supplied by the caller is ignored: the source is whatever the repository
// service has stored for this project.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		GitSHA    string          `json:"git_sha"`
		GitBranch string          `json:"git_branch"`
		RepoID    string          `json:"repo_id"`
		Strategy  string          `json:"strategy"`
		Message   string          `json:"message"`
		JPConfig  json.RawMessage `json:"jp_config"`
		CloneURL  json.RawMessage `json:"clone_url"` // accepted for old clients, ignored
		FullName  json.RawMessage `json:"full_name"` // ignored
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		return
	}
	if req.GitSHA == "" {
		req.GitSHA = "HEAD"
	}
	if !shaRe.MatchString(req.GitSHA) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "git_sha must be a commit id or HEAD")
		return
	}
	if req.GitBranch != "" && (!branchRe.MatchString(req.GitBranch) || strings.HasPrefix(req.GitBranch, "-") || strings.Contains(req.GitBranch, "..")) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid git_branch")
		return
	}
	if len(req.JPConfig) > 0 && (!json.Valid(req.JPConfig) || !bytes.HasPrefix(bytes.TrimSpace(req.JPConfig), []byte("{"))) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "jp_config must be a JSON object")
		return
	}
	if len(req.Message) > 500 {
		req.Message = req.Message[:500]
	}
	repos, err := h.repos(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "repository service unavailable")
		return
	}
	if len(repos) == 0 {
		httpx.Error(w, http.StatusBadRequest, "no_repository", "connect a repository to this project first")
		return
	}
	repo := repos[0]
	if req.RepoID != "" {
		found := false
		for _, rp := range repos {
			if rp.ID == req.RepoID {
				repo, found = rp, true
			}
		}
		if !found {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "repo_id is not connected to this project")
			return
		}
	}
	if req.GitBranch == "" {
		req.GitBranch = repo.DefaultBranch
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = StrategyFromConfig(req.JPConfig)
	}
	d := &store.Deployment{OrgID: orgID, ProjectID: projectID, Status: store.Queued, Source: "api", Strategy: normalizeStrategy(strategy),
		JPConfig: req.JPConfig, GitSHA: req.GitSHA, GitBranch: req.GitBranch, FullName: repo.FullName, Message: req.Message, CreatedBy: &uid}
	rid := repo.ID
	d.RepoID = &rid
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create deployment")
		return
	}
	h.postCommitStatus(d)
	h.startBuild(r.Context(), d)
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

// Cancel stops a deployment that has not finished.
func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	d, ok := h.load(w, r, orgID, projectID)
	if !ok {
		return
	}
	switch d.Status {
	case store.Queued, store.Building, store.Deploying:
	default:
		httpx.Error(w, http.StatusConflict, "conflict", "deployment is "+d.Status+" and cannot be canceled")
		return
	}
	if d.BuildID != nil && (d.Status == store.Queued || d.Status == store.Building) {
		_, _ = h.call(r.Context(), http.MethodPost, base(h.BuildURL)+"/internal/builds/"+url.PathEscape(*d.BuildID)+"/cancel", nil, nil)
	}
	nd, err := h.transition(r.Context(), d.ID, store.Patch{Status: store.Canceled, Error: "canceled by user"})
	if errors.Is(err, store.ErrBadTransition) {
		httpx.Error(w, http.StatusConflict, "conflict", "deployment changed state; reload")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cancel failed")
		return
	}
	var errs []string
	if d.Status == store.Deploying {
		errs = h.stopRuntime(r.Context(), d.ID)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployment": nd, "cleanup_errors": errs})
}

// Rollback re-deploys the previous live production version.
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	list, err := h.Store.RollbackCandidates(r.Context(), orgID, projectID, 2)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if len(list) < 2 {
		httpx.Error(w, http.StatusNotFound, "not_found", "no earlier successful deployment to roll back to")
		return
	}
	h.createRollback(w, r, uid, &list[1])
}

// RollbackTo re-deploys a specific earlier production deployment.
func (h *Handler) RollbackTo(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	prev, ok := h.load(w, r, orgID, projectID)
	if !ok {
		return
	}
	if prev.IsPreview || prev.ImageRef == "" || (prev.Status != store.Ready && prev.Status != store.Superseded) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "target is not a successfully deployed production version")
		return
	}
	h.createRollback(w, r, uid, prev)
}

func (h *Handler) createRollback(w http.ResponseWriter, r *http.Request, uid string, prev *store.Deployment) {
	strategy := normalizeStrategy(prev.Strategy)
	if r.ContentLength > 0 {
		var body struct {
			Strategy string `json:"strategy"`
		}
		if err := httpx.Decode(r, &body); err == nil && body.Strategy != "" {
			strategy = normalizeStrategy(body.Strategy)
		}
	}
	pid := prev.ID
	d := &store.Deployment{OrgID: prev.OrgID, ProjectID: prev.ProjectID, Status: store.Deploying, Source: "rollback", Strategy: strategy,
		JPConfig: prev.JPConfig, GitSHA: prev.GitSHA, GitBranch: prev.GitBranch, FullName: prev.FullName, Message: "rollback to " + prev.ID,
		ImageRef: prev.ImageRef, CreatedBy: &uid, RollbackOf: &pid, RepoID: prev.RepoID, BuildID: prev.BuildID}
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create rollback deployment")
		return
	}
	// "deploying" until the runtime really started the old image and it passed its health gate
	h.postCommitStatus(d)
	h.publishDeploy(r.Context(), d)
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

// ---------------------------------------------------------------- internal API

// FromGit is called by the repository service for webhook events.
func (h *Handler) FromGit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		RepoID    string `json:"repo_id"`
		GitSHA    string `json:"git_sha"`
		GitBranch string `json:"git_branch"`
		PRNumber  int    `json:"pr_number"`
		Message   string `json:"message"`
		Source    string `json:"source"`
		Preview   bool   `json:"preview"`
		Fork      bool   `json:"fork"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid payload (clone_url is not accepted)")
		return
	}
	for _, id := range []string{req.OrgID, req.ProjectID, req.RepoID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id and repo_id must be UUIDs")
			return
		}
	}
	if !shaRe.MatchString(req.GitSHA) || req.GitSHA == "HEAD" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "git_sha must be a commit id")
		return
	}
	if req.PRNumber < 0 || (req.PRNumber > 0 && !req.Preview) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "pr_number requires preview")
		return
	}
	if req.GitBranch == "" && req.PRNumber > 0 {
		req.GitBranch = fmt.Sprintf("pr-%d", req.PRNumber)
	}
	if !branchRe.MatchString(req.GitBranch) || strings.HasPrefix(req.GitBranch, "-") || strings.Contains(req.GitBranch, "..") {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid git_branch")
		return
	}
	repos, err := h.repos(r.Context(), req.OrgID, req.ProjectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "repository service unavailable")
		return
	}
	var repo *repoInfo
	for i := range repos {
		if repos[i].ID == req.RepoID {
			repo = &repos[i]
		}
	}
	if repo == nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "repo_id is not connected to this project")
		return
	}
	if req.Source == "" {
		req.Source = "webhook"
	}
	if len(req.Message) > 500 {
		req.Message = req.Message[:500]
	}
	d := &store.Deployment{OrgID: req.OrgID, ProjectID: req.ProjectID, Status: store.Queued, Source: req.Source, Strategy: "rolling",
		GitSHA: req.GitSHA, GitBranch: req.GitBranch, FullName: repo.FullName, Message: req.Message, IsPreview: req.Preview,
		PRNumber: req.PRNumber, Fork: req.Fork}
	rid := repo.ID
	d.RepoID = &rid
	var older []store.Deployment
	if req.Preview {
		older, _ = h.Store.ActivePreviews(r.Context(), req.OrgID, req.ProjectID, req.PRNumber, req.GitBranch)
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create deployment")
		return
	}
	h.postCommitStatus(d)
	h.startBuild(r.Context(), d)
	for i := range older { // a new push to the same PR replaces its previous preview
		if errs := h.teardown(r.Context(), &older[i]); len(errs) > 0 {
			h.Log.Warn("superseded preview teardown incomplete", "deployment", older[i].ID, "errors", errs)
		}
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

// InternalUpdateStatus is the single place where worker / scheduler move a deployment.
func (h *Handler) InternalUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deploymentId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	}
	var req struct {
		Status     string          `json:"status"`
		ImageRef   string          `json:"image_ref"`
		Error      string          `json:"error"`
		BuildID    string          `json:"build_id"`
		GitSHA     string          `json:"git_sha"`
		JPConfig   json.RawMessage `json:"jp_config"`
		PreviewURL *string         `json:"preview_url"`
		Framework  string          `json:"framework"`
	}
	if err := httpx.DecodeLimit(r, &req, 1<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid payload")
		return
	}
	if req.Status != "" && !store.ValidStatus(req.Status) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "unknown status")
		return
	}
	if req.BuildID != "" {
		if _, err := uuid.Parse(req.BuildID); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid build_id")
			return
		}
	}
	if req.GitSHA != "" && !shaRe.MatchString(req.GitSHA) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid git_sha")
		return
	}
	if len(req.JPConfig) > 0 && (!json.Valid(req.JPConfig) || string(req.JPConfig) == "null") {
		req.JPConfig = nil
	}
	if len(req.Error) > 1000 {
		req.Error = req.Error[:1000]
	}
	_, err := h.transition(r.Context(), id, store.Patch{Status: req.Status, ImageRef: req.ImageRef, Error: req.Error, BuildID: req.BuildID,
		GitSHA: req.GitSHA, JPConfig: req.JPConfig, PreviewURL: req.PreviewURL})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
	case errors.Is(err, store.ErrBadTransition):
		httpx.Error(w, http.StatusConflict, "conflict", "invalid status transition")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) InternalSetPreviewURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deploymentId")
	var req struct {
		PreviewURL string `json:"preview_url"`
	}
	if err := httpx.Decode(r, &req); err != nil || len(req.PreviewURL) > 500 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "preview_url required")
		return
	}
	u := strings.TrimSpace(req.PreviewURL)
	if _, err := h.transition(r.Context(), id, store.Patch{PreviewURL: &u}); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stopRuntime asks the runtime to stop and remove the deployment's containers.
func (h *Handler) stopRuntime(ctx context.Context, deploymentID string) (errs []string) {
	code, err := h.call(ctx, http.MethodPost, base(h.RuntimeURL)+"/internal/runtime/stop-by-deployment", map[string]any{"deployment_id": deploymentID}, nil)
	if err != nil || code >= 300 {
		errs = append(errs, fmt.Sprintf("runtime stop failed (status %d, err %v)", code, err))
	}
	return errs
}

// teardown really removes everything a preview owns: containers, preview domain, image
// tag; then records the final state. Returns the failures (never silently ignored).
func (h *Handler) teardown(ctx context.Context, d *store.Deployment) (errs []string) {
	errs = append(errs, h.stopRuntime(ctx, d.ID)...)
	if h.DomainURL != "" {
		code, err := h.call(ctx, http.MethodDelete, base(h.DomainURL)+"/internal/domains/by-deployment/"+url.PathEscape(d.ID), nil, nil)
		if err != nil || (code >= 300 && code != http.StatusNotFound) {
			errs = append(errs, fmt.Sprintf("preview domain removal failed (status %d, err %v)", code, err))
		}
	}
	if d.IsPreview && d.ImageRef != "" && h.RegistryURL != "" {
		code, err := h.call(ctx, http.MethodPost, base(h.RegistryURL)+"/internal/images/delete", map[string]any{
			"org_id": d.OrgID, "project_id": d.ProjectID, "image_ref": d.ImageRef}, nil)
		if err != nil || code >= 300 {
			errs = append(errs, fmt.Sprintf("image tag removal failed (status %d, err %v)", code, err))
		}
	}
	if d.Status == store.Queued || d.Status == store.Building {
		if d.BuildID != nil {
			_, _ = h.call(ctx, http.MethodPost, base(h.BuildURL)+"/internal/builds/"+url.PathEscape(*d.BuildID)+"/cancel", nil, nil)
		}
	}
	empty := ""
	target := store.Stopped
	if d.Status != store.Ready && d.Status != store.Superseded {
		target = store.Canceled
	}
	if _, err := h.transition(ctx, d.ID, store.Patch{Status: target, Error: "preview closed", PreviewURL: &empty}); err != nil && !errors.Is(err, store.ErrBadTransition) {
		errs = append(errs, "state update failed: "+err.Error())
	}
	return errs
}

func (h *Handler) InternalPreviewTeardown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		FullName  string `json:"full_name"`
		GitBranch string `json:"git_branch"`
		PRNumber  int    `json:"pr_number"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" || (req.PRNumber <= 0 && req.GitBranch == "") {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id and pr_number or git_branch required")
		return
	}
	var list []store.Deployment
	var err error
	if req.PRNumber > 0 {
		list, err = h.Store.PreviewsByPR(r.Context(), req.OrgID, req.ProjectID, req.FullName, req.PRNumber)
	} else {
		list, err = h.Store.ActivePreviews(r.Context(), req.OrgID, req.ProjectID, 0, req.GitBranch)
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	var all []string
	for i := range list {
		all = append(all, h.teardown(r.Context(), &list[i])...)
	}
	status := http.StatusOK
	if len(all) > 0 {
		status = http.StatusMultiStatus
	}
	httpx.JSON(w, status, map[string]any{"matched": len(list), "torn_down": len(list), "errors": all})
}

func (h *Handler) CleanupPreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OlderThanHours float64 `json:"older_than_hours"`
	}
	_ = httpx.Decode(r, &req)
	hours := req.OlderThanHours
	if hours <= 0 {
		hours = 72
	}
	list, err := h.Store.ExpiredPreviews(r.Context(), time.Now().UTC().Add(-time.Duration(hours*float64(time.Hour))))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	var all []string
	for i := range list {
		all = append(all, h.teardown(r.Context(), &list[i])...)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"expired": len(list), "errors": all, "older_than_hours": hours})
}

// InternalDeleteProject cancels in-flight builds and removes the project's deployment rows.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	live, err := h.Store.LiveByProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	var errs []string
	for _, d := range live {
		if d.BuildID != nil && (d.Status == store.Queued || d.Status == store.Building) {
			if code, err := h.call(r.Context(), http.MethodPost, base(h.BuildURL)+"/internal/builds/"+url.PathEscape(*d.BuildID)+"/cancel", nil, nil); err != nil || code >= 500 {
				errs = append(errs, "build cancel failed for "+d.ID)
			}
		}
	}
	n, err := h.Store.DeleteProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	status := http.StatusOK
	if len(errs) > 0 {
		status = http.StatusMultiStatus
	}
	httpx.JSON(w, status, map[string]any{"deployments_removed": n, "errors": errs})
}
