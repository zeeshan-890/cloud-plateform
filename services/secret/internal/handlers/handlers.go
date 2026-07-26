package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/secret/internal/crypto"
	"github.com/jp-cloud/secret/internal/store"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	Crypto          *crypto.Envelope
	JWT             *jwtutil.Manager
	OrganizationURL string
	Redis           *redis.Client
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/environments", auth(http.HandlerFunc(h.ListEnvironments)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/environments", auth(http.HandlerFunc(h.CreateEnvironment)))

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets", auth(http.HandlerFunc(h.ListSecrets)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets", auth(http.HandlerFunc(h.CreateSecret)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(h.GetSecret)))
	mux.Handle("PUT /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(h.SetSecret)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(h.DeleteSecret)))

	// Env convenience aliases (list / set / unset)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/env/{env}", auth(http.HandlerFunc(h.ListSecrets)))
	mux.Handle("PUT /orgs/{orgId}/projects/{projectId}/env/{env}/{name}", auth(http.HandlerFunc(h.SetSecretAlias)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/env/{env}/{name}", auth(http.HandlerFunc(h.DeleteSecret)))

	// Internal decrypt for runtime injection (never exposed via gateway)
	mux.HandleFunc("GET /internal/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}/value", h.InternalValue)
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

func validEnv(env string) bool {
	return store.ValidEnvironments[strings.ToLower(strings.TrimSpace(env))]
}

func (h *Handler) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.ListEnvironments(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list environments")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"environments": list})
}

func (h *Handler) CreateEnvironment(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validEnv(req.Name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name must be development|preview|staging|production")
		return
	}
	e, err := h.Store.CreateEnvironment(r.Context(), orgID, projectID, req.Name)
	if err == store.ErrConflict {
		httpx.Error(w, http.StatusConflict, "conflict", "environment already exists or invalid")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create environment")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, e.Name, "", "environment.create", uid, middleware.RequestIDFrom(r.Context()), nil)
	httpx.JSON(w, http.StatusCreated, map[string]any{"environment": e})
}

func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if !validEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	_ = h.Store.EnsureDefaultEnvironments(r.Context(), orgID, projectID)
	list, err := h.Store.ListSecrets(r.Context(), orgID, projectID, env)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list secrets")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"secrets": list, "environment": env})
}

func (h *Handler) CreateSecret(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if !validEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" || req.Value == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name and value required")
		return
	}
	name := normalizeSecretName(req.Name)
	ct, nonce, err := h.Crypto.Encrypt([]byte(req.Value))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "encrypt failed")
		return
	}
	hint := crypto.Hint(req.Value)
	meta, err := h.Store.CreateSecret(r.Context(), orgID, projectID, env, name, hint, ct, nonce, h.Crypto.KeyVersion, uid)
	if err == store.ErrConflict {
		httpx.Error(w, http.StatusConflict, "conflict", "secret already exists; use PUT to rotate")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create secret")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.create", uid, middleware.RequestIDFrom(r.Context()), map[string]any{
		"version": 1, "hint": hint,
	})
	h.publish(r, uid, orgID, "secret.created", map[string]any{
		"project_id": projectID, "environment": env, "name": name, "version": 1,
	})
	// Never return plaintext value
	httpx.JSON(w, http.StatusCreated, map[string]any{"secret": meta})
}

func (h *Handler) GetSecret(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	name := normalizeSecretName(r.PathValue("name"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	versions, _ := h.Store.ListVersions(r.Context(), meta.ID)
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.read_meta", uid, middleware.RequestIDFrom(r.Context()), nil)
	httpx.JSON(w, http.StatusOK, map[string]any{"secret": meta, "versions": versions})
}

func (h *Handler) SetSecret(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	name := normalizeSecretName(r.PathValue("name"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if !validEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.Value == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "value required")
		return
	}
	hint := crypto.Hint(req.Value)
	ct, nonce, err := h.Crypto.Encrypt([]byte(req.Value))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "encrypt failed")
		return
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if err == store.ErrNotFound {
		meta, err = h.Store.CreateSecret(r.Context(), orgID, projectID, env, name, hint, ct, nonce, h.Crypto.KeyVersion, uid)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to set secret")
			return
		}
		h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.create", uid, middleware.RequestIDFrom(r.Context()), map[string]any{"version": 1})
		httpx.JSON(w, http.StatusCreated, map[string]any{"secret": meta})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	meta, err = h.Store.UpdateSecret(r.Context(), meta, hint, ct, nonce, h.Crypto.KeyVersion, uid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to rotate secret")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.rotate", uid, middleware.RequestIDFrom(r.Context()), map[string]any{
		"version": meta.CurrentVersion, "hint": hint,
	})
	h.publish(r, uid, orgID, "secret.rotated", map[string]any{
		"project_id": projectID, "environment": env, "name": name, "version": meta.CurrentVersion,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"secret": meta})
}

func (h *Handler) SetSecretAlias(w http.ResponseWriter, r *http.Request) {
	h.SetSecret(w, r)
}

func (h *Handler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	name := normalizeSecretName(r.PathValue("name"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if err := h.Store.DeleteSecret(r.Context(), orgID, projectID, env, name); err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.delete", uid, middleware.RequestIDFrom(r.Context()), nil)
	h.publish(r, uid, orgID, "secret.deleted", map[string]any{
		"project_id": projectID, "environment": env, "name": name,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) InternalValue(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	env := strings.ToLower(r.PathValue("env"))
	name := normalizeSecretName(r.PathValue("name"))
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	ct, nonce, err := h.Store.GetCiphertext(r.Context(), meta.ID, meta.CurrentVersion)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "version missing")
		return
	}
	plain, err := h.Crypto.Decrypt(ct, nonce)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "decrypt failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"name": name, "environment": env, "version": meta.CurrentVersion, "value": string(plain),
	})
}

func (h *Handler) publish(r *http.Request, actor, org, typ string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicSecret, typ, actor, org, payload)
	_, _ = events.PublishJSON(r.Context(), h.Redis, events.TopicSecret, env)
}

func normalizeSecretName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "_")
	return name
}
