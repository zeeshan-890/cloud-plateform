package handlers

import (
	"net/http"
	"strings"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/scheduler/internal/cron"
)

type Handler struct {
	Cron *cron.Store
	JWT  *jwtutil.Manager
	Slot string
}

func (h *Handler) Routes(mux *http.ServeMux) {
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(h.ListCron)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(h.CreateCron)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/cron/{cronId}", auth(http.HandlerFunc(h.DeleteCron)))

	// Optional user-facing queue stub
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/queues", auth(http.HandlerFunc(h.ListQueues)))
}

func (h *Handler) ListCron(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	list, err := h.Cron.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"schedules": list})
}

func (h *Handler) CreateCron(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	var req struct {
		Name     string `json:"name"`
		Cron     string `json:"cron"`
		ImageRef string `json:"image_ref"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required")
		return
	}
	expr := strings.TrimSpace(req.Cron)
	if expr == "" {
		expr = "@hourly"
	}
	sch := &cron.Schedule{
		OrgID: orgID, ProjectID: projectID, Name: strings.TrimSpace(req.Name),
		Cron: expr, ImageRef: strings.TrimSpace(req.ImageRef),
	}
	if err := h.Cron.Create(r.Context(), sch); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"schedule": sch})
}

func (h *Handler) DeleteCron(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	cronID := r.PathValue("cronId")
	if err := h.Cron.Delete(r.Context(), orgID, projectID, cronID); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "schedule not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListQueues(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{
		"queues": []map[string]any{
			{"name": "jp.build", "kind": "platform", "description": "Build farm jobs"},
			{"name": "jp.cleanup", "kind": "platform", "description": "Orphan image / preview cleanup"},
			{"name": "jp.jobs", "kind": "platform", "description": "Cron / background runtime jobs"},
		},
		"mode": "stub",
		"note": "User-facing custom queues are a stub; platform uses Redis Streams.",
	})
}
