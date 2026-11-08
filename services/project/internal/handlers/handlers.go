package handlers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/go-common/jpconfig"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/project/internal/store"
)

// CleanupTarget is a service that owns per-project resources and implements
// DELETE /internal/projects/{projectId}.
type CleanupTarget struct {
	Name string
	URL  string
}

type Handler struct {
	Store   store.Repo
	JWT     *jwtutil.Manager
	Members *membership.Client
	HTTP    *http.Client // internal client signed with INTERNAL_TOKEN
	Log     *slog.Logger

	OrganizationURL string
	DomainURL       string
	// Cleanup lists the services called (in order) on project delete.
	Cleanup []CleanupTarget

	inflight sync.Map // projectID -> struct{} (deletions in progress in this process)
}

// slugRe: 1-30 chars, lowercase alnum and hyphen, no leading/trailing hyphen. The slug becomes
// part of DNS names and a docker network name, so it is strict.
var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,28}[a-z0-9])?$`)

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return internalauth.NewClient(30 * time.Second)
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
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/config/drift", auth(http.HandlerFunc(h.ConfigDrift)))

	// Internal (INTERNAL_TOKEN required)
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("GET /internal/orgs/{orgId}/projects", in(h.InternalList))
	mux.Handle("GET /internal/orgs/{orgId}/projects/{projectId}", in(h.InternalGet))
	mux.Handle("DELETE /internal/orgs/{orgId}/projects/{projectId}", in(h.InternalDelete))
}

func cleanName(n string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.TrimSpace(n) {
		if r < 32 || r == 127 {
			continue
		}
		if r == ' ' || r == '\t' {
			if space {
				continue
			}
			space = true
		} else {
			space = false
		}
		b.WriteRune(r)
	}
	out := b.String()
	if rs := []rune(out); len(rs) > 100 {
		out = string(rs[:100])
	}
	return out
}

func cleanDescription(d string) string {
	d = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(d))
	if rs := []rune(d); len(rs) > 500 {
		d = string(rs[:500])
	}
	return d
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	// Creating projects is an org owner/admin action.
	if _, ok := h.Members.Require(w, r, orgID, uid, membership.Admin); !ok {
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
	req.Name = cleanName(req.Name)
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if req.Name == "" || req.Slug == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name and slug required")
		return
	}
	if !slugRe.MatchString(req.Slug) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "slug must be 1-30 characters: lowercase letters, digits and hyphens, not starting or ending with a hyphen")
		return
	}
	ctx := r.Context()
	p, err := h.Store.Create(ctx, orgID, req.Name, req.Slug, cleanDescription(req.Description))
	if errors.Is(err, store.ErrAlreadyExists) {
		httpx.Error(w, http.StatusConflict, "conflict", "project slug already exists in org")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create project")
		return
	}
	// Register the system domain (<project-slug>-<org-slug>.<ROOT_DOMAIN>). On failure the project
	// is rolled back so we never keep a project that cannot be reached.
	if err := h.registerSystemDomain(ctx, p); err != nil {
		h.log().Error("system domain registration failed; rolling back project", "project", p.ID, "err", err)
		if derr := h.Store.Delete(context.WithoutCancel(ctx), orgID, p.ID); derr != nil {
			h.log().Error("rollback failed", "project", p.ID, "err", derr)
		}
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "project could not be created: system domain registration failed: "+err.Error())
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"project": p})
}

func (h *Handler) registerSystemDomain(ctx context.Context, p *store.Project) error {
	if h.DomainURL == "" {
		return nil
	}
	orgSlug := ""
	if h.OrganizationURL != "" {
		slug, err := h.lookupOrgSlug(ctx, p.OrgID)
		if err != nil {
			return fmt.Errorf("organization lookup: %w", err)
		}
		orgSlug = slug
	}
	body, _ := json.Marshal(map[string]string{"org_id": p.OrgID, "org_slug": orgSlug, "project_id": p.ID, "project_slug": p.Slug})
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(h.DomainURL, "/")+"/internal/domains/system", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("domain service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

func (h *Handler) lookupOrgSlug(ctx context.Context, orgID string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, fmt.Sprintf("%s/internal/orgs/%s", strings.TrimRight(h.OrganizationURL, "/"), orgID), nil)
	if err != nil {
		return "", err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var out struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil || out.Slug == "" {
		return "", errors.New("organization slug unavailable")
	}
	return out.Slug, nil
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if _, ok := h.Members.Require(w, r, orgID, uid, membership.Viewer); !ok {
		return
	}
	projects, err := h.Store.List(r.Context(), orgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list projects")
		return
	}
	// Restricted users only see the projects they were granted.
	scope, member, err := h.Members.AccessibleProjects(r, orgID, uid)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "access check unavailable")
		return
	}
	if !member {
		httpx.Error(w, http.StatusForbidden, "forbidden", "not a member of this organization")
		return
	}
	if !scope.Full {
		kept := projects[:0]
		for _, p := range projects {
			if scope.Allows(p.ID) {
				kept = append(kept, p)
			}
		}
		projects = kept
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if _, ok := h.Members.RequireArea(w, r, orgID, r.PathValue("projectId"), uid, membership.AreaOverview, membership.LevelView); !ok {
		return
	}
	p, err := h.Store.Get(r.Context(), orgID, r.PathValue("projectId"))
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
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaSettings, membership.LevelManage); !ok {
		return
	}
	var req struct {
		Name        *string `json:"name"`
		Slug        *string `json:"slug"`
		Description *string `json:"description"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	if req.Name != nil {
		n := cleanName(*req.Name)
		if n == "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "name cannot be empty")
			return
		}
		req.Name = &n
	}
	if req.Description != nil {
		d := cleanDescription(*req.Description)
		req.Description = &d
	}
	if req.Slug != nil {
		cur, err := h.Store.Get(r.Context(), orgID, projectID)
		if errors.Is(err, store.ErrNotFound) {
			httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
			return
		}
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update project")
			return
		}
		if strings.ToLower(strings.TrimSpace(*req.Slug)) != cur.Slug {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "slug cannot be changed after creation (it determines the project's hostnames)")
			return
		}
	}
	p, err := h.Store.Update(r.Context(), orgID, projectID, req.Name, req.Description)
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
	case errors.Is(err, store.ErrDeleting):
		httpx.Error(w, http.StatusConflict, "conflict", "project is being deleted")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update project")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"project": p})
	}
}

// Delete (admin+) orchestrates the cascade across services; see cascadeDelete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaSettings, membership.LevelManage); !ok {
		return
	}
	h.runDelete(w, r, orgID, projectID)
}

func (h *Handler) InternalDelete(w http.ResponseWriter, r *http.Request) {
	h.runDelete(w, r, r.PathValue("orgId"), r.PathValue("projectId"))
}

func (h *Handler) runDelete(w http.ResponseWriter, r *http.Request, orgID, projectID string) {
	// The cascade must finish even if the client disconnects.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 8*time.Minute)
	defer cancel()
	failures, err := h.CascadeDelete(ctx, orgID, projectID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
	case errors.Is(err, errInProgress):
		httpx.Error(w, http.StatusConflict, "conflict", "project deletion already in progress")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete project")
	case len(failures) > 0:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":    map[string]string{"code": "partial_failure", "message": "some resources could not be cleaned up; the project is kept in status 'deleting' - retry the delete"},
			"failures": failures,
		})
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

var errInProgress = errors.New("deletion in progress")

// Failure is one service that could not clean up.
type Failure struct {
	Service string `json:"service"`
	Error   string `json:"error"`
}

// CascadeDelete marks the project `deleting`, calls DELETE /internal/projects/{id} on every
// cleanup target in order, aggregates failures (never silently ignored), and only removes the
// project row when every target succeeded. A 404 from a target means "nothing to delete".
func (h *Handler) CascadeDelete(ctx context.Context, orgID, projectID string) ([]Failure, error) {
	if _, loaded := h.inflight.LoadOrStore(projectID, struct{}{}); loaded {
		return nil, errInProgress
	}
	defer h.inflight.Delete(projectID)

	if _, err := h.Store.MarkDeleting(ctx, orgID, projectID); err != nil {
		return nil, err
	}
	var failures []Failure
	for _, t := range h.Cleanup {
		if t.URL == "" {
			continue
		}
		if err := h.cleanupOne(ctx, t, projectID); err != nil {
			h.log().Error("project cleanup failed", "service", t.Name, "project", projectID, "err", err)
			failures = append(failures, Failure{Service: t.Name, Error: err.Error()})
		}
	}
	if len(failures) > 0 {
		parts := make([]string, 0, len(failures))
		for _, f := range failures {
			parts = append(parts, f.Service+": "+f.Error)
		}
		_ = h.Store.SetDeleteError(ctx, orgID, projectID, strings.Join(parts, "; "))
		return failures, nil
	}
	if err := h.Store.Delete(ctx, orgID, projectID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return nil, nil
}

func (h *Handler) cleanupOne(ctx context.Context, t CleanupTarget, projectID string) error {
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodDelete, fmt.Sprintf("%s/internal/projects/%s", strings.TrimRight(t.URL, "/"), projectID), nil)
	if err != nil {
		return err
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	switch {
	case resp.StatusCode/100 == 2, resp.StatusCode == http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

// ---------------------------------------------------------------- jp.yaml config

func (h *Handler) ApplyConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaSettings, membership.LevelManage); !ok {
		return
	}
	var req struct {
		Raw      string             `json:"raw"`
		Config   map[string]any     `json:"config"`
		Manifest *jpconfig.Manifest `json:"manifest"`
	}
	if err := httpx.DecodeLimit(r, &req, 2<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
		return
	}
	m, err := manifestFrom(req.Manifest, req.Config)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
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
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
	case errors.Is(err, store.ErrDeleting):
		httpx.Error(w, http.StatusConflict, "conflict", "project is being deleted")
	case err != nil:
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to apply config")
	default:
		httpx.JSON(w, http.StatusOK, map[string]any{"project": p, "config": cfg, "strategy": m.Strategy(), "hash": hash})
	}
}

func manifestFrom(m *jpconfig.Manifest, cfg map[string]any) (*jpconfig.Manifest, error) {
	if m != nil {
		return m, nil
	}
	if len(cfg) == 0 {
		return nil, errors.New("config or manifest required")
	}
	b, _ := json.Marshal(cfg)
	out := &jpconfig.Manifest{}
	if err := json.Unmarshal(b, out); err != nil {
		return nil, errors.New("invalid config object")
	}
	return out, nil
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaSettings, membership.LevelView); !ok {
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
	httpx.JSON(w, http.StatusOK, map[string]any{"config": cfg, "raw": p.JPConfigRaw, "hash": p.JPConfigHash, "applied_at": p.JPConfigAppliedAt})
}

// ConfigDrift compares a desired config with the last-applied one. POST a {config|manifest}
// body for a key-by-key comparison, or GET ?desired_hash=<sha256 of raw jp.yaml> for a hash check.
func (h *Handler) ConfigDrift(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaSettings, membership.LevelView); !ok {
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
	base := map[string]any{"applied_hash": p.JPConfigHash, "applied_at": p.JPConfigAppliedAt}
	if r.Method == http.MethodPost {
		var req struct {
			Config   map[string]any     `json:"config"`
			Manifest *jpconfig.Manifest `json:"manifest"`
		}
		if err := httpx.DecodeLimit(r, &req, 2<<20); err != nil {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON")
			return
		}
		m, err := manifestFrom(req.Manifest, req.Config)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		drift, details := jpconfig.Drift(m.ToMap(), applied)
		base["drift"], base["details"], base["method"] = drift, details, "config-compare"
		httpx.JSON(w, http.StatusOK, base)
		return
	}
	desired := r.URL.Query().Get("desired_hash")
	if desired == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "provide ?desired_hash=<sha256 of jp.yaml> or POST the desired config")
		return
	}
	drift := desired != p.JPConfigHash
	base["drift"], base["desired_hash"], base["method"] = drift, desired, "hash-compare"
	if drift {
		base["details"] = []string{"desired_hash differs from applied hash"}
	} else {
		base["details"] = []string{}
	}
	httpx.JSON(w, http.StatusOK, base)
}

// ---------------------------------------------------------------- internal

func (h *Handler) InternalList(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.List(r.Context(), r.PathValue("orgId"))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, p := range list {
		out = append(out, map[string]any{"id": p.ID, "slug": p.Slug, "name": p.Name, "status": p.Status})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"projects": out})
}

// InternalGet is the project<->org binding oracle for membership.RequireArea: 200 when the
// project exists in the org and is not being deleted, otherwise 404.
func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	p, err := h.Store.Get(r.Context(), r.PathValue("orgId"), r.PathValue("projectId"))
	if errors.Is(err, store.ErrNotFound) || (err == nil && p.Status == store.StatusDeleting) {
		httpx.Error(w, http.StatusNotFound, "not_found", "project not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"id": p.ID, "org_id": p.OrgID, "slug": p.Slug, "name": p.Name, "status": p.Status})
}
