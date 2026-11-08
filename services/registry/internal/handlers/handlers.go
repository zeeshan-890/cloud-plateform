package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/registry/internal/regclient"
	"github.com/jp-cloud/registry/internal/store"
)

// Repo is the persistence used by the handlers (implemented by *store.Store).
type Repo interface {
	Create(ctx context.Context, img *store.Image) error
	List(ctx context.Context, orgID, projectID string) ([]store.Image, error)
	Candidates(ctx context.Context, olderThan time.Time, keepPerProject int, keepRefs map[string]struct{}) ([]store.Image, error)
	ListAllByProject(ctx context.Context, projectID string) ([]store.Image, error)
	Delete(ctx context.Context, id string) error
	DeleteByRef(ctx context.Context, projectID, ref string) error
}

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

// Deleter removes an image from the registry.
type Deleter interface {
	Delete(ctx context.Context, ref string) error
}

type Handler struct {
	Store          Repo
	JWT            *jwtutil.Manager
	Authz          Authorizer
	Registry       Deleter
	KeepPerProject int // newest images per project that are never removed (rollback targets)
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/images", auth(http.HandlerFunc(h.List)))
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/images", in(h.InternalCreate))
	mux.Handle("POST /internal/cleanup/orphaned-images", in(h.CleanupOrphans))
	mux.Handle("POST /internal/images/delete", in(h.InternalDeleteImage))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	for _, id := range []string{orgID, projectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
			return
		}
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, middleware.UserIDFrom(r.Context()), membership.AreaBuilds, membership.LevelView); !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list images")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"images": list})
}

func (h *Handler) InternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		ImageRef  string `json:"image_ref"`
		Framework string `json:"framework"`
		GitSHA    string `json:"git_sha"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id and image_ref required")
		return
	}
	for _, id := range []string{req.OrgID, req.ProjectID} {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and project_id must be UUIDs")
			return
		}
	}
	if len(req.ImageRef) > 255 || len(req.Framework) > 40 || len(req.GitSHA) > 64 {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "field too long")
		return
	}
	img := &store.Image{OrgID: req.OrgID, ProjectID: req.ProjectID, ImageRef: req.ImageRef, Framework: req.Framework, GitSHA: req.GitSHA}
	if err := h.Store.Create(r.Context(), img); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to register image")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"image": img})
}

// CleanupOrphans deletes old, unreferenced images from the registry (manifest DELETE) and
// only then drops their metadata. The newest KeepPerProject images of every project and
// every ref in keep_refs are always kept.
func (h *Handler) CleanupOrphans(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OlderThanHours float64  `json:"older_than_hours"`
		KeepRefs       []string `json:"keep_refs"`
	}
	_ = httpx.Decode(r, &req)
	hours := req.OlderThanHours
	if hours <= 0 {
		hours = 168
	}
	keep := map[string]struct{}{}
	for _, ref := range req.KeepRefs {
		keep[ref] = struct{}{}
	}
	n := h.KeepPerProject
	if n <= 0 {
		n = 5
	}
	cutoff := time.Now().UTC().Add(-time.Duration(hours * float64(time.Hour)))
	cands, err := h.Store.Candidates(r.Context(), cutoff, n, keep)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	res := h.deleteImages(r.Context(), cands)
	status := http.StatusOK
	if len(res.Errors) > 0 {
		status = http.StatusMultiStatus
	}
	httpx.JSON(w, status, map[string]any{"deleted": res.Deleted, "failed": len(res.Errors), "errors": res.Errors, "older_than_hours": hours, "kept_per_project": n})
}

type deleteResult struct {
	Deleted int
	Errors  []string
}

func (h *Handler) deleteImages(ctx context.Context, imgs []store.Image) deleteResult {
	var res deleteResult
	for _, img := range imgs {
		err := h.Registry.Delete(ctx, img.ImageRef)
		if err != nil && !errors.Is(err, regclient.ErrForeign) {
			res.Errors = append(res.Errors, img.ImageRef+": "+err.Error())
			continue // keep the metadata so the delete is retried next run
		}
		// ErrForeign: the ref is not in our registry (e.g. pushed elsewhere); only metadata is dropped
		if err := h.Store.Delete(ctx, img.ID); err != nil {
			res.Errors = append(res.Errors, img.ImageRef+": metadata: "+err.Error())
			continue
		}
		res.Deleted++
	}
	return res
}

// InternalDeleteProject removes every image of a deleted project from the registry.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	imgs, err := h.Store.ListAllByProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	res := h.deleteImages(r.Context(), imgs)
	status := http.StatusOK
	if len(res.Errors) > 0 {
		status = http.StatusMultiStatus
	}
	httpx.JSON(w, status, map[string]any{"deleted": res.Deleted, "failed": len(res.Errors), "errors": res.Errors})
}

// InternalDeleteImage removes one image tag (preview teardown) from the registry.
func (h *Handler) InternalDeleteImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		ImageRef  string `json:"image_ref"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "project_id and image_ref required")
		return
	}
	if _, err := uuid.Parse(req.ProjectID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "project_id must be a UUID")
		return
	}
	if err := h.Registry.Delete(r.Context(), req.ImageRef); err != nil && !errors.Is(err, regclient.ErrForeign) {
		httpx.Error(w, http.StatusBadGateway, "registry_error", err.Error())
		return
	}
	if err := h.Store.DeleteByRef(r.Context(), req.ProjectID, req.ImageRef); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "metadata removal failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
