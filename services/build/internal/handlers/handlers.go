package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/google/uuid"
	"github.com/jp-cloud/build/internal/store"
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
	nameRe   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
)

// Repo is the persistence used by the handlers (implemented by *store.Store).
type Repo interface {
	Create(ctx context.Context, b *store.Build) error
	List(ctx context.Context, orgID, projectID string) ([]store.Build, error)
	Get(ctx context.Context, id string) (*store.Build, error)
	Apply(ctx context.Context, id string, u store.Update) (*store.Build, bool, error)
	DeleteProject(ctx context.Context, projectID string) (int64, error)
}

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store Repo
	JWT   *jwtutil.Manager
	Authz Authorizer
	Redis *redis.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}/logs", auth(http.HandlerFunc(h.Logs)))

	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/builds", in(h.InternalCreate))
	mux.Handle("GET /internal/builds/{buildId}", in(h.InternalGet))
	mux.Handle("POST /internal/builds/{buildId}/status", in(h.InternalStatus))
	mux.Handle("POST /internal/builds/{buildId}/cancel", in(h.InternalCancel))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (orgID, projectID string, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	for _, id := range []string{orgID, projectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
	}
	if _, ok = h.Authz.RequireArea(w, r, orgID, projectID, middleware.UserIDFrom(r.Context()), membership.AreaBuilds, membership.LevelView); !ok {
		return
	}
	return orgID, projectID, true
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list builds")
		return
	}
	for i := range list { // list view: summary only, details via GET
		list[i].Scan = nil
		list[i].Logs = ""
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"builds": list})
}

func (h *Handler) load(w http.ResponseWriter, r *http.Request) (*store.Build, bool) {
	orgID, projectID, ok := h.authorize(w, r)
	if !ok {
		return nil, false
	}
	id := r.PathValue("buildId")
	if _, err := uuid.Parse(id); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return nil, false
	}
	b, err := h.Store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (b.OrgID != orgID || b.ProjectID != projectID)) {
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return nil, false
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return nil, false
	}
	return b, true
}

// Get returns the build including logs and the vulnerability scan summary.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	if b, ok := h.load(w, r); ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"build": b})
	}
}

func (h *Handler) Logs(w http.ResponseWriter, r *http.Request) {
	if b, ok := h.load(w, r); ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"build_id": b.ID, "status": b.Status, "logs": b.Logs, "truncated": b.LogsTruncated})
	}
}

func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	b, err := h.Store.Get(r.Context(), r.PathValue("buildId"))
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	b.Logs = ""
	httpx.JSON(w, http.StatusOK, map[string]any{"build": b})
}

func (h *Handler) InternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID        string `json:"org_id"`
		ProjectID    string `json:"project_id"`
		DeploymentID string `json:"deployment_id"`
		RepoID       string `json:"repo_id"`
		GitSHA       string `json:"git_sha"`
		GitBranch    string `json:"git_branch"`
		PRNumber     int    `json:"pr_number"`
		FullName     string `json:"full_name"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid payload")
		return
	}
	for _, id := range []string{req.OrgID, req.ProjectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and project_id must be UUIDs")
			return
		}
	}
	for _, id := range []string{req.DeploymentID, req.RepoID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				httpx.Error(w, http.StatusBadRequest, "bad_request", "deployment_id/repo_id must be UUIDs")
				return
			}
		}
	}
	if req.GitBranch != "" && (!branchRe.MatchString(req.GitBranch) || req.GitBranch[0] == '-') {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid git_branch")
		return
	}
	if req.GitSHA != "" && !shaRe.MatchString(req.GitSHA) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid git_sha")
		return
	}
	if req.FullName != "" && !nameRe.MatchString(req.FullName) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid full_name")
		return
	}
	if req.PRNumber < 0 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid pr_number")
		return
	}
	b := &store.Build{OrgID: req.OrgID, ProjectID: req.ProjectID, Status: "queued", GitSHA: req.GitSHA, GitBranch: req.GitBranch,
		FullName: req.FullName, PRNumber: req.PRNumber, Logs: "queued\n"}
	if req.DeploymentID != "" {
		b.DeploymentID = &req.DeploymentID
	}
	if req.RepoID != "" {
		b.RepoID = &req.RepoID
	}
	if err := h.Store.Create(r.Context(), b); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create build")
		return
	}
	if err := h.publishJob(r.Context(), b); err != nil {
		_, _, _ = h.Store.Apply(r.Context(), b.ID, store.Update{Status: "failed", LogsAppend: "enqueue error\n", Error: "failed to enqueue build"})
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to enqueue build")
		return
	}
	b.Logs = ""
	httpx.JSON(w, http.StatusCreated, map[string]any{"build": b})
}

var validStatus = map[string]bool{"running": true, "succeeded": true, "failed": true, "canceled": true}

func (h *Handler) InternalStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("buildId")
	var req struct {
		Status     string          `json:"status"`
		Framework  string          `json:"framework"`
		ImageRef   string          `json:"image_ref"`
		Logs       string          `json:"logs"` // chunk to append
		Error      string          `json:"error"`
		ScanStatus string          `json:"scan_status"`
		Scan       json.RawMessage `json:"scan"`
	}
	if err := httpx.DecodeLimit(r, &req, 2<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if req.Status != "" && !validStatus[req.Status] {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid status")
		return
	}
	if len(req.Logs) > 512<<10 {
		req.Logs = req.Logs[len(req.Logs)-512<<10:]
	}
	b, changed, err := h.Store.Apply(r.Context(), id, store.Update{Status: req.Status, Framework: req.Framework, ImageRef: req.ImageRef,
		LogsAppend: req.Logs, Error: req.Error, ScanStatus: req.ScanStatus, Scan: req.Scan})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return
	case errors.Is(err, store.ErrTerminal):
		httpx.Error(w, http.StatusConflict, "conflict", "build already finished")
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	if changed {
		h.publishBuildOutcome(r.Context(), b)
	}
	w.WriteHeader(http.StatusNoContent)
}

// InternalCancel cancels a queued/running build; the worker notices and aborts.
func (h *Handler) InternalCancel(w http.ResponseWriter, r *http.Request) {
	b, changed, err := h.Store.Apply(r.Context(), r.PathValue("buildId"), store.Update{Status: "canceled", LogsAppend: "canceled\n", Error: "canceled"})
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "build not found")
		return
	case errors.Is(err, store.ErrTerminal):
		httpx.JSON(w, http.StatusOK, map[string]any{"canceled": false, "status": b.Status})
		return
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "cancel failed")
		return
	}
	if changed {
		h.publishBuildOutcome(r.Context(), b)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"canceled": changed, "status": b.Status})
}

// publishBuildOutcome emits build.succeeded / build.failed exactly once per build
// (the store only reports changed=true for the transition itself).
func (h *Handler) publishBuildOutcome(ctx context.Context, b *store.Build) {
	if h.Redis == nil || b == nil {
		return
	}
	typ := events.TypeBuildFailed
	if b.Status == "succeeded" {
		typ = events.TypeBuildSucceeded
	}
	if ok, err := h.Redis.SetNX(ctx, "jp:build:evt:"+b.ID+":"+b.Status, "1", 24*60*60*1e9).Result(); err != nil || !ok {
		return
	}
	depID := ""
	if b.DeploymentID != nil {
		depID = *b.DeploymentID
	}
	// outcomes go to jp.deploy: the worker group on jp.build must only ever see build jobs
	duration := 0.0
	if b.StartedAt != nil && b.FinishedAt != nil {
		duration = b.FinishedAt.Sub(*b.StartedAt).Seconds()
	}
	env := events.New(events.TopicDeploy, typ, "", b.OrgID, map[string]any{
		"duration_seconds": duration, "build_id": b.ID, "org_id": b.OrgID, "project_id": b.ProjectID, "deployment_id": depID,
		"image_ref": b.ImageRef, "framework": b.Framework, "status": b.Status, "git_sha": b.GitSHA, "scan_status": b.ScanStatus,
	})
	_, _ = events.PublishJSON(ctx, h.Redis, events.TopicDeploy, env)
}

func (h *Handler) publishJob(ctx context.Context, b *store.Build) error {
	if h.Redis == nil {
		return fmt.Errorf("redis not configured")
	}
	if err := events.EnsureGroup(ctx, h.Redis, events.TopicBuild, events.BuildConsumerGroup); err != nil {
		return err
	}
	depID, repoID := "", ""
	if b.DeploymentID != nil {
		depID = *b.DeploymentID
	}
	if b.RepoID != nil {
		repoID = *b.RepoID
	}
	env := events.New(events.TopicBuild, events.TypeBuildQueued, "", b.OrgID, map[string]any{
		"build_id": b.ID, "org_id": b.OrgID, "project_id": b.ProjectID, "deployment_id": depID, "repo_id": repoID,
		"git_sha": b.GitSHA, "git_branch": b.GitBranch, "pr_number": b.PRNumber, "full_name": b.FullName,
	})
	_, err := events.PublishJSON(ctx, h.Redis, events.TopicBuild, env)
	return err
}

// InternalDeleteProject removes all builds of a deleted project.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	n, err := h.Store.DeleteProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"builds_removed": n})
}
