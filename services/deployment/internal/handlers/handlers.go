package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/deployment/internal/store"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	BuildURL        string
	RepositoryURL   string
	RuntimeURL      string
	DomainURL       string
	PublicBaseURL   string
	HTTP            *http.Client
	Log             *slog.Logger
	Redis           *redis.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/deployments", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/deployments", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/deployments/rollback", auth(http.HandlerFunc(h.Rollback)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}/rollback", auth(http.HandlerFunc(h.RollbackTo)))

	mux.HandleFunc("POST /internal/deployments/from-git", h.FromGit)
	mux.HandleFunc("POST /internal/deployments/{deploymentId}/status", h.InternalUpdateStatus)
	mux.HandleFunc("POST /internal/deployments/{deploymentId}/preview-url", h.InternalSetPreviewURL)
	mux.HandleFunc("POST /internal/preview/teardown", h.InternalPreviewTeardown)
	mux.HandleFunc("POST /internal/cleanup/preview-deploys", h.CleanupPreview)
}

func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request, orgID, userID string) bool {
	if h.OrganizationURL == "" {
		return true
	}
	url := fmt.Sprintf("%s/internal/orgs/%s/members/%s", strings.TrimRight(h.OrganizationURL, "/"), orgID, userID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "membership check failed")
		return false
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "organization service unavailable")
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member")
		return false
	}
	return resp.StatusCode == http.StatusOK
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		GitSHA    string          `json:"git_sha"`
		GitBranch string          `json:"git_branch"`
		CloneURL  string          `json:"clone_url"`
		FullName  string          `json:"full_name"`
		Message   string          `json:"message"`
		RepoID    string          `json:"repo_id"`
		Strategy  string          `json:"strategy"`
		JPConfig  json.RawMessage `json:"jp_config"`
	}
	_ = httpx.Decode(r, &req)
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	if req.GitSHA == "" {
		req.GitSHA = "HEAD"
	}
	strategy := normalizeStrategy(req.Strategy)
	d := &store.Deployment{
		OrgID: orgID, ProjectID: projectID, Status: "queued", Source: "api",
		Strategy: strategy, JPConfig: req.JPConfig,
		GitSHA: req.GitSHA, GitBranch: req.GitBranch, CloneURL: req.CloneURL,
		FullName: req.FullName, Message: req.Message, CommitStatus: "pending", CreatedBy: &uid,
	}
	if req.RepoID != "" {
		d.RepoID = &req.RepoID
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create deployment")
		return
	}
	h.postCommitStatus(d, "pending")
	h.startBuild(r.Context(), d)
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list deployments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployments": list})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("deploymentId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deployment": d})
}

// Rollback creates a new deployment pointing at the previous successful deploy.
func (h *Handler) Rollback(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	prev, err := h.Store.LatestSuccessful(r.Context(), orgID, projectID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "no successful deployment to roll back to")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	h.createRollback(w, r, uid, prev)
}

// RollbackTo rolls back to a specific successful deployment ID.
func (h *Handler) RollbackTo(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	targetID := r.PathValue("deploymentId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	prev, err := h.Store.Get(r.Context(), orgID, projectID, targetID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "deployment not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if prev.Status != "ready" || prev.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "target deployment is not a successful ready deploy")
		return
	}
	h.createRollback(w, r, uid, prev)
}

func (h *Handler) createRollback(w http.ResponseWriter, r *http.Request, uid string, prev *store.Deployment) {
	strategy := normalizeStrategy(r.URL.Query().Get("strategy"))
	if strategy == "rolling" && prev.Strategy != "" {
		strategy = normalizeStrategy(prev.Strategy)
	}
	if r.ContentLength != 0 {
		var body struct {
			Strategy string `json:"strategy"`
		}
		if err := httpx.Decode(r, &body); err == nil && body.Strategy != "" {
			strategy = normalizeStrategy(body.Strategy)
		}
	}
	prevID := prev.ID
	d := &store.Deployment{
		OrgID: prev.OrgID, ProjectID: prev.ProjectID, Status: "ready", Source: "rollback",
		Strategy: strategy, JPConfig: prev.JPConfig,
		GitSHA: prev.GitSHA, GitBranch: prev.GitBranch, CloneURL: prev.CloneURL,
		FullName: prev.FullName, Message: "rollback to " + prev.ID, ImageRef: prev.ImageRef,
		CommitStatus: "success", CreatedBy: &uid, RollbackOf: &prevID, RepoID: prev.RepoID, BuildID: prev.BuildID,
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create rollback deployment")
		return
	}
	h.postCommitStatus(d, "success")
	h.publishDeploy(r.Context(), d)
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

func (h *Handler) FromGit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string          `json:"org_id"`
		ProjectID string          `json:"project_id"`
		RepoID    string          `json:"repo_id"`
		GitSHA    string          `json:"git_sha"`
		GitBranch string          `json:"git_branch"`
		CloneURL  string          `json:"clone_url"`
		FullName  string          `json:"full_name"`
		Message   string          `json:"message"`
		Source    string          `json:"source"`
		Strategy  string          `json:"strategy"`
		JPConfig  json.RawMessage `json:"jp_config"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and project_id required")
		return
	}
	if req.Source == "" {
		req.Source = "webhook"
	}
	if req.GitBranch == "" {
		req.GitBranch = "main"
	}
	d := &store.Deployment{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Status: "queued", Source: req.Source,
		Strategy: normalizeStrategy(req.Strategy), JPConfig: req.JPConfig,
		GitSHA: req.GitSHA, GitBranch: req.GitBranch, CloneURL: req.CloneURL,
		FullName: req.FullName, Message: req.Message, CommitStatus: "pending",
	}
	if req.RepoID != "" {
		d.RepoID = &req.RepoID
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create deployment")
		return
	}
	h.postCommitStatus(d, "pending")
	h.startBuild(r.Context(), d)
	httpx.JSON(w, http.StatusCreated, map[string]any{"deployment": d})
}

func (h *Handler) InternalUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deploymentId")
	var req struct {
		Status       string  `json:"status"`
		CommitStatus string  `json:"commit_status"`
		ImageRef     string  `json:"image_ref"`
		Error        string  `json:"error"`
		BuildID      *string `json:"build_id"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.Status == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "status required")
		return
	}
	if req.CommitStatus == "" {
		switch req.Status {
		case "ready":
			req.CommitStatus = "success"
		case "failed":
			req.CommitStatus = "failure"
		default:
			req.CommitStatus = "pending"
		}
	}
	if err := h.Store.UpdateStatus(r.Context(), id, req.Status, req.CommitStatus, req.ImageRef, req.Error, req.BuildID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	if d, err := h.Store.GetByID(r.Context(), id); err == nil {
		h.postCommitStatus(d, req.CommitStatus)
		if req.Status == "ready" || req.Status == "failed" {
			h.publishDeploy(r.Context(), d)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) startBuild(ctx context.Context, d *store.Deployment) {
	buildID := h.enqueueBuild(d)
	if buildID == "" {
		_ = h.Store.UpdateStatus(ctx, d.ID, "failed", "failure", "", "failed to enqueue build", nil)
		d.Status = "failed"
		d.CommitStatus = "failure"
		d.Error = "failed to enqueue build"
		h.postCommitStatus(d, "failure")
		return
	}
	_ = h.Store.UpdateStatus(ctx, d.ID, "building", "pending", "", "", &buildID)
	d.BuildID = &buildID
	d.Status = "building"
}

func (h *Handler) enqueueBuild(d *store.Deployment) string {
	if h.BuildURL == "" || h.HTTP == nil {
		return ""
	}
	body, _ := json.Marshal(map[string]any{
		"org_id":        d.OrgID,
		"project_id":    d.ProjectID,
		"deployment_id": d.ID,
		"git_sha":       d.GitSHA,
		"git_branch":    d.GitBranch,
		"clone_url":     d.CloneURL,
		"full_name":     d.FullName,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.BuildURL, "/")+"/internal/builds", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("enqueue build failed", "err", err)
		}
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	var out struct {
		Build struct {
			ID string `json:"id"`
		} `json:"build"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Build.ID
}

// postCommitStatus asks the repository service to publish a GitHub commit status
// (no-op when the GitHub App is not configured).
func (h *Handler) postCommitStatus(d *store.Deployment, state string) {
	if d == nil {
		return
	}
	if h.Log != nil {
		h.Log.Info("commit status", "deployment_id", d.ID, "sha", d.GitSHA, "state", state, "repo", d.FullName)
	}
	if h.RepositoryURL == "" || h.HTTP == nil {
		return
	}
	sha := strings.TrimSpace(d.GitSHA)
	if sha == "" || sha == "HEAD" || d.FullName == "" {
		return
	}
	target := strings.TrimRight(h.PublicBaseURL, "/")
	if target == "" {
		target = "http://localhost:8000"
	}
	if d.PreviewURL != "" {
		target = d.PreviewURL
	}
	desc := "jp deployment " + state
	body, _ := json.Marshal(map[string]any{
		"full_name":   d.FullName,
		"sha":         sha,
		"state":       state,
		"description": desc,
		"target_url":  target,
	})
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.RepositoryURL, "/")+"/internal/github/commit-status", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		if h.Log != nil {
			h.Log.Warn("commit status request failed", "err", err)
		}
		return
	}
	_ = resp.Body.Close()
}

func (h *Handler) publishDeploy(ctx context.Context, d *store.Deployment) {
	if h.Redis == nil || d == nil {
		return
	}
	buildID := ""
	if d.BuildID != nil {
		buildID = *d.BuildID
	}
	env := events.New(events.TopicDeploy, events.TypeDeployUpdated, "", d.OrgID, map[string]any{
		"deployment_id": d.ID,
		"org_id":        d.OrgID,
		"project_id":    d.ProjectID,
		"status":        d.Status,
		"image_ref":     d.ImageRef,
		"build_id":      buildID,
		"source":        d.Source,
		"git_sha":       d.GitSHA,
		"git_branch":    d.GitBranch,
		"message":       d.Message,
		"preview":       store.IsPreviewDeploy(d),
		"preview_url":   d.PreviewURL,
		"strategy":      normalizeStrategy(d.Strategy),
	})
	_, _ = events.PublishJSON(ctx, h.Redis, events.TopicDeploy, env)
}

func normalizeStrategy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "blue_green", "blue-green", "bluegreen":
		return "blue_green"
	default:
		return "rolling"
	}
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
	cutoff := time.Now().UTC().Add(-time.Duration(hours * float64(time.Hour)))
	ids, err := h.Store.ExpirePreviewDeploys(r.Context(), cutoff)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	tornDown := 0
	for _, id := range ids {
		if h.teardownResources(r.Context(), id) {
			tornDown++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"expired": len(ids), "torn_down": tornDown, "older_than_hours": hours, "ids": ids,
	})
}

func (h *Handler) InternalSetPreviewURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("deploymentId")
	var req struct {
		PreviewURL string `json:"preview_url"`
	}
	if err := httpx.Decode(r, &req); err != nil || id == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "preview_url required")
		return
	}
	if err := h.Store.SetPreviewURL(r.Context(), id, strings.TrimSpace(req.PreviewURL)); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	if d, err := h.Store.GetByID(r.Context(), id); err == nil {
		d.PreviewURL = strings.TrimSpace(req.PreviewURL)
		if d.Status == "ready" {
			h.postCommitStatus(d, "success")
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalPreviewTeardown(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		FullName  string `json:"full_name"`
		GitBranch string `json:"git_branch"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.GitBranch == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "git_branch required")
		return
	}
	list, err := h.Store.FindPreviewByBranch(r.Context(), req.FullName, req.GitBranch)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	torn := 0
	for _, d := range list {
		if req.OrgID != "" && d.OrgID != req.OrgID {
			continue
		}
		if req.ProjectID != "" && d.ProjectID != req.ProjectID {
			continue
		}
		_ = h.Store.UpdateStatus(r.Context(), d.ID, "failed", "failure", "", "preview closed", nil)
		_ = h.Store.SetPreviewURL(r.Context(), d.ID, "")
		if h.teardownResources(r.Context(), d.ID) {
			torn++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"matched": len(list), "torn_down": torn, "git_branch": req.GitBranch,
	})
}

func (h *Handler) teardownResources(ctx context.Context, deploymentID string) bool {
	ok := false
	if h.RuntimeURL != "" && h.HTTP != nil {
		body, _ := json.Marshal(map[string]any{"deployment_id": deploymentID})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(h.RuntimeURL, "/")+"/internal/runtime/stop-by-deployment", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if resp, err := h.HTTP.Do(req); err == nil {
				_ = resp.Body.Close()
				ok = true
			}
		}
	}
	if h.DomainURL != "" && h.HTTP != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
			strings.TrimRight(h.DomainURL, "/")+"/internal/domains/by-deployment/"+deploymentID, nil)
		if err == nil {
			if resp, err := h.HTTP.Do(req); err == nil {
				_ = resp.Body.Close()
				ok = true
			}
		}
	}
	return ok
}
