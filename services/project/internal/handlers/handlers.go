package handlers

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/jpconfig"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/project/internal/store"
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

	mux.Handle("POST /orgs/{orgId}/projects", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("PATCH /orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(h.Update)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(h.Delete)))

	mux.Handle("PUT /orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(h.ApplyConfig)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(h.ApplyConfig)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(h.GetConfig)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/config/drift", auth(http.HandlerFunc(h.ConfigDrift)))
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
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return false
	}
	if resp.StatusCode != http.StatusOK {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "organization service error")
		return false
	}
	return true
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name and slug required")
		return
	}
	p, err := h.Store.Create(r.Context(), orgID, req.Name, req.Slug, req.Description)
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "project slug already exists in org")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create project")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"project": p})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	projects, err := h.Store.List(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list projects")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	p, err := h.Store.Get(r.Context(), orgID, projectID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to get project")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project": p})
}

func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if req.Slug != nil {
		s := strings.ToLower(strings.TrimSpace(*req.Slug))
		req.Slug = &s
	}
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		req.Name = &n
	}
	p, err := h.Store.Update(r.Context(), orgID, projectID, req.Name, req.Slug, req.Description)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "project slug already exists in org")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update project")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project": p})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if err := h.Store.Delete(r.Context(), orgID, projectID); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete project")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ApplyConfig stores last-applied jp.yaml desired state (validated against jp-schema rules).
func (h *Handler) ApplyConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Raw      string         `json:"raw"`
		Config   map[string]any `json:"config"`
		Manifest *jpconfig.Manifest `json:"manifest"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}

	var m *jpconfig.Manifest
	if req.Manifest != nil {
		m = req.Manifest
	} else if len(req.Config) > 0 {
		b, _ := json.Marshal(req.Config)
		m = &jpconfig.Manifest{}
		if err := json.Unmarshal(b, m); err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid config object")
			return
		}
	} else {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "config or manifest required")
		return
	}
	if err := jpconfig.Validate(m); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	cfg := m.ToMap()
	raw := req.Raw
	if raw == "" {
		b, _ := json.MarshalIndent(cfg, "", "  ")
		raw = string(b)
	}
	sum := sha256.Sum256([]byte(raw))
	hash := hex.EncodeToString(sum[:])
	p, err := h.Store.ApplyConfig(r.Context(), orgID, projectID, raw, hash, cfg)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to apply config")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"project":  p,
		"config":   cfg,
		"strategy": m.Strategy(),
		"hash":     hash,
	})
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	p, err := h.Store.Get(r.Context(), orgID, projectID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	var cfg map[string]any
	if len(p.JPConfig) > 0 {
		_ = json.Unmarshal(p.JPConfig, &cfg)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"config":     cfg,
		"raw":        p.JPConfigRaw,
		"hash":       p.JPConfigHash,
		"applied_at": p.JPConfigAppliedAt,
	})
}

// ConfigDrift is a stub: compares optional desired body to last-applied config.
func (h *Handler) ConfigDrift(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	p, err := h.Store.Get(r.Context(), orgID, projectID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	var applied map[string]any
	if len(p.JPConfig) > 0 {
		_ = json.Unmarshal(p.JPConfig, &applied)
	}
	desired := applied
	if r.Method == http.MethodGet {
		if q := r.URL.Query().Get("desired_hash"); q != "" && q != p.JPConfigHash {
			httpx.JSON(w, http.StatusOK, map[string]any{
				"drift": true, "stub": true,
				"details":     []string{"desired_hash differs from applied"},
				"applied_hash": p.JPConfigHash,
				"desired_hash": q,
			})
			return
		}
	}
	drift, details := jpconfig.DriftStub(desired, applied)
	httpx.JSON(w, http.StatusOK, map[string]any{
		"drift":        drift,
		"stub":         true,
		"details":      details,
		"applied":      applied,
		"applied_hash": p.JPConfigHash,
		"applied_at":   p.JPConfigAppliedAt,
		"message":      "Drift detection is a Phase-7 stub (shallow key compare / hash).",
	})
}
