package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/registry/internal/store"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/images", auth(http.HandlerFunc(h.List)))
	mux.HandleFunc("POST /internal/images", h.InternalCreate)
	mux.HandleFunc("POST /internal/cleanup/orphaned-images", h.CleanupOrphans)
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
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ImageRef == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and image_ref required")
		return
	}
	img := &store.Image{
		OrgID: req.OrgID, ProjectID: req.ProjectID, ImageRef: req.ImageRef,
		Framework: req.Framework, GitSHA: req.GitSHA,
	}
	if err := h.Store.Create(r.Context(), img); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to register image")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"image": img})
}

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
		if ref != "" {
			keep[ref] = struct{}{}
		}
	}
	// Also keep any image referenced in the last 50 rows per recent created — soft safety via keep_refs only
	cutoff := time.Now().UTC().Add(-time.Duration(hours * float64(time.Hour)))
	n, err := h.Store.DeleteOrphans(r.Context(), cutoff, keep)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"deleted": n, "older_than_hours": hours})
}
