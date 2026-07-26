package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/build/internal/store"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	Redis           *redis.Client
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}/logs", auth(http.HandlerFunc(h.Logs)))

	mux.HandleFunc("POST /internal/builds", h.InternalCreate)
	mux.HandleFunc("POST /internal/builds/{buildId}/status", h.InternalStatus)
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

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list builds")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"builds": list})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	buildID := r.PathValue("buildId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	b, err := h.Store.Get(r.Context(), buildID)
	if errors.Is(err, store.ErrNotFound) || (b != nil && (b.OrgID != orgID || b.ProjectID != projectID)) {
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"build": b})
}

func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	buildID := r.PathValue("buildId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	b, err := h.Store.Get(r.Context(), buildID)
	if errors.Is(err, store.ErrNotFound) || (b != nil && (b.OrgID != orgID || b.ProjectID != projectID)) {
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"build_id": b.ID,
		"status":   b.Status,
		"logs":     b.Logs,
	})
}

func (h *Handler) InternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID        string `json:"org_id"`
		ProjectID    string `json:"project_id"`
		DeploymentID string `json:"deployment_id"`
		GitSHA       string `json:"git_sha"`
		GitBranch    string `json:"git_branch"`
		CloneURL     string `json:"clone_url"`
		FullName     string `json:"full_name"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and project_id required")
		return
	}
	b := &store.Build{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Status: "queued",
		GitSHA: req.GitSHA, GitBranch: req.GitBranch, CloneURL: req.CloneURL, FullName: req.FullName,
		Logs: "queued\n",
	}
	if req.DeploymentID != "" {
		b.DeploymentID = &req.DeploymentID
	}
	if err := h.Store.Create(r.Context(), b); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create build")
		return
	}
	if err := h.publishJob(r.Context(), b); err != nil {
		_ = h.Store.AppendUpdate(r.Context(), b.ID, "failed", "", "", "enqueue error: "+err.Error()+"\n", err.Error(), false, true)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to enqueue build")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"build": b})
}

func (h *Handler) InternalStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("buildId")
	var req struct {
		Status    string `json:"status"`
		Framework string `json:"framework"`
		ImageRef  string `json:"image_ref"`
		Logs      string `json:"logs"`
		Error     string `json:"error"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	started := req.Status == "running"
	finished := req.Status == "succeeded" || req.Status == "failed"
	if err := h.Store.AppendUpdate(r.Context(), id, req.Status, req.Framework, req.ImageRef, req.Logs, req.Error, started, finished); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	if req.Status == "succeeded" || req.Status == "failed" {
		if b, err := h.Store.Get(r.Context(), id); err == nil {
			h.publishBuildOutcome(r.Context(), b)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) publishBuildOutcome(ctx context.Context, b *store.Build) {
	if h.Redis == nil {
		return
	}
	typ := events.TypeBuildFailed
	if b.Status == "succeeded" {
		typ = events.TypeBuildSucceeded
	}
	depID := ""
	if b.DeploymentID != nil {
		depID = *b.DeploymentID
	}
	payload := map[string]any{
		"build_id": b.ID, "org_id": b.OrgID, "project_id": b.ProjectID,
		"deployment_id": depID, "image_ref": b.ImageRef, "framework": b.Framework,
		"status": b.Status, "git_sha": b.GitSHA,
	}
	// jp.deploy stream is consumed by the Phase-4 scheduler for runtime start.
	env := events.New(events.TopicDeploy, typ, "", b.OrgID, payload)
	_, _ = events.PublishJSON(ctx, h.Redis, events.TopicDeploy, env)
}

func (h *Handler) publishJob(ctx context.Context, b *store.Build) error {
	if h.Redis == nil {
		return fmt.Errorf("redis not configured")
	}
	if err := events.EnsureGroup(ctx, h.Redis, events.TopicBuild, events.BuildConsumerGroup); err != nil {
		return err
	}
	depID := ""
	if b.DeploymentID != nil {
		depID = *b.DeploymentID
	}
	env := events.New(events.TopicBuild, events.TypeBuildQueued, "", b.OrgID, map[string]any{
		"build_id":      b.ID,
		"org_id":        b.OrgID,
		"project_id":    b.ProjectID,
		"deployment_id": depID,
		"git_sha":       b.GitSHA,
		"git_branch":    b.GitBranch,
		"clone_url":     b.CloneURL,
		"full_name":     b.FullName,
	})
	_, err := events.PublishJSON(ctx, h.Redis, events.TopicBuild, env)
	return err
}
