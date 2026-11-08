package handlers

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/scheduler/internal/cron"
)

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Cron  *cron.Store
	JWT   *jwtutil.Manager
	Authz Authorizer
}

func (h *Handler) Routes(mux *http.ServeMux) {
	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(h.ListCron)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(h.CreateCron)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/cron/{cronId}", auth(http.HandlerFunc(h.DeleteCron)))

	mux.Handle("DELETE /internal/projects/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteProject)))
}

func validIDs(w http.ResponseWriter, ids ...string) bool {
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
			return false
		}
	}
	return true
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID string, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	if !validIDs(w, orgID, projectID) {
		return
	}
	if min != membership.LevelView {
		if c, has := middleware.ClaimsFrom(r.Context()); has && c.ReadOnly() {
			httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
			return
		}
	}
	if _, ok = h.Authz.RequireArea(w, r, orgID, projectID, middleware.UserIDFrom(r.Context()), membership.AreaRuntime, min); !ok {
		return
	}
	return orgID, projectID, true
}

func (h *Handler) ListCron(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	list, err := h.Cron.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"schedules": list})
}

func (h *Handler) CreateCron(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		Name           string   `json:"name"`
		Cron           string   `json:"cron"`
		ImageRef       string   `json:"image_ref"`
		Command        []string `json:"command"`
		TimeoutSeconds int      `json:"timeout_seconds"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		return
	}
	sch := &cron.Schedule{
		OrgID: orgID, ProjectID: projectID, Name: req.Name, Cron: req.Cron, ImageRef: req.ImageRef,
		Command: req.Command, TimeoutSeconds: req.TimeoutSeconds,
	}
	if err := cron.Validate(sch); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if err := h.Cron.Create(r.Context(), sch); err != nil {
		if errors.Is(err, cron.ErrLimit) {
			httpx.Error(w, http.StatusConflict, "limit_reached", err.Error())
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"schedule": sch})
}

func (h *Handler) DeleteCron(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	cronID := r.PathValue("cronId")
	if !validIDs(w, cronID) {
		return
	}
	if err := h.Cron.Delete(r.Context(), orgID, projectID, cronID); err != nil {
		if errors.Is(err, cron.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "schedule not found")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// InternalDeleteProject removes all schedules of a deleted project.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if !validIDs(w, projectID) {
		return
	}
	n, err := h.Cron.DeleteProject(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"schedules_removed": n})
}
