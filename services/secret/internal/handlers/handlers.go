package handlers

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/secret/internal/crypto"
	"github.com/jp-cloud/secret/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	MaxValueBytes = 64 << 10
	maxBody       = MaxValueBytes + 4096
)

var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// Repo is the persistence used by the handlers (implemented by *store.Store).
type Repo interface {
	EnsureDefaultEnvironments(ctx context.Context, orgID, projectID string) error
	ListEnvironments(ctx context.Context, orgID, projectID string) ([]store.Environment, error)
	CreateEnvironment(ctx context.Context, orgID, projectID, name string) (*store.Environment, error)
	ListSecrets(ctx context.Context, orgID, projectID, env string) ([]store.SecretMeta, error)
	GetSecret(ctx context.Context, orgID, projectID, env, name string) (*store.SecretMeta, error)
	CreateSecret(ctx context.Context, orgID, projectID, env, name, hint string, ct, nonce []byte, keyVersion int, actorID string) (*store.SecretMeta, error)
	UpdateSecretWithRename(ctx context.Context, meta *store.SecretMeta, newName, hint string, ct, nonce []byte, keyVersion int, actorID string) (*store.SecretMeta, error)
	DeleteSecret(ctx context.Context, orgID, projectID, env, name string) error
	ListVersions(ctx context.Context, secretID string) ([]store.SecretVersionMeta, error)
	GetCiphertext(ctx context.Context, secretID string, version int) ([]byte, []byte, int, error)
	Audit(ctx context.Context, orgID, projectID, env, secretName, action, actorID, requestID string, meta map[string]any)
	ListAudit(ctx context.Context, orgID, projectID string, limit, offset int) ([]store.AuditEntry, error)
	EnvRows(ctx context.Context, orgID, projectID string, envs []string) ([]store.EnvRow, error)
	DeleteProject(ctx context.Context, projectID string) (int64, error)
	OutdatedVersions(ctx context.Context, currentKeyVersion, limit int, projectID string) ([]store.VersionRow, error)
	SetCiphertext(ctx context.Context, versionID string, ct, nonce []byte, keyVersion int) error
}

// Authorizer enforces org membership + project binding.
type Authorizer interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store  Repo
	Crypto *crypto.Envelope
	JWT    *jwtutil.Manager
	Authz  Authorizer
	Redis  *redis.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	u := func(f http.HandlerFunc) http.Handler { return auth(f) }
	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }

	const p = "/orgs/{orgId}/projects/{projectId}"
	mux.Handle("GET "+p+"/environments", u(h.ListEnvironments))
	mux.Handle("POST "+p+"/environments", u(h.CreateEnvironment))
	mux.Handle("GET "+p+"/secrets", u(h.ListAllSecrets))
	mux.Handle("GET "+p+"/secrets/audit", u(h.ListAudit))
	mux.Handle("POST "+p+"/secrets/reencrypt", u(h.ReencryptProject))
	mux.Handle("GET "+p+"/environments/{env}/secrets", u(h.ListSecrets))
	mux.Handle("POST "+p+"/environments/{env}/secrets", u(h.CreateSecret))
	mux.Handle("GET "+p+"/environments/{env}/secrets/{name}", u(h.GetSecret))
	mux.Handle("PUT "+p+"/environments/{env}/secrets/{name}", u(h.SetSecret))
	mux.Handle("DELETE "+p+"/environments/{env}/secrets/{name}", u(h.DeleteSecret))
	mux.Handle("GET "+p+"/env/{env}", u(h.ListSecrets))
	mux.Handle("PUT "+p+"/env/{env}/{name}", u(h.SetSecret))
	mux.Handle("DELETE "+p+"/env/{env}/{name}", u(h.DeleteSecret))

	mux.Handle("GET /internal/orgs/{orgId}/projects/{projectId}/env", in(h.InternalEnv))
	mux.Handle("GET /internal/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}/value", in(h.InternalValue))
	mux.Handle("PUT /internal/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", in(h.InternalUpsert))
	mux.Handle("POST /internal/rotate-master-key", in(h.InternalRotate))
	mux.Handle("DELETE /internal/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", in(h.InternalDeleteSecret))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

// ---------------------------------------------------------------- validation

func validEnv(env string) bool { return store.ValidEnvironments[env] }

// validSecretEnv also allows the shared "all" scope (overridden by the specific environment).
func validSecretEnv(env string) bool { return env == "all" || validEnv(env) }

func validName(n string) bool { return nameRe.MatchString(n) }

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID, uid string, ok bool) {
	orgID, projectID, uid, _, ok = h.authorizeRole(w, r, min)
	return
}

func (h *Handler) authorizeRole(w http.ResponseWriter, r *http.Request, min membership.Level) (orgID, projectID, uid string, acc *membership.Access, ok bool) {
	orgID, projectID = r.PathValue("orgId"), r.PathValue("projectId")
	if _, err := uuid.Parse(orgID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid organization id")
		return
	}
	if _, err := uuid.Parse(projectID); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	if min != membership.LevelView {
		if c, has := middleware.ClaimsFrom(r.Context()); has && c.ReadOnly() {
			httpx.Error(w, http.StatusForbidden, "forbidden", "read-only token")
			return
		}
	}
	uid = middleware.UserIDFrom(r.Context())
	if acc, ok = h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaSecrets, min); !ok {
		return
	}
	return orgID, projectID, uid, acc, true
}

func (h *Handler) rid(r *http.Request) string { return middleware.RequestIDFrom(r.Context()) }

// ---------------------------------------------------------------- environments

func (h *Handler) ListEnvironments(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
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
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validEnv(strings.ToLower(strings.TrimSpace(req.Name))) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name must be development|preview|staging|production")
		return
	}
	e, err := h.Store.CreateEnvironment(r.Context(), orgID, projectID, req.Name)
	if errors.Is(err, store.ErrConflict) {
		httpx.Error(w, http.StatusConflict, "conflict", "environment already exists")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create environment")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, e.Name, "", "environment.create", uid, h.rid(r), nil)
	httpx.JSON(w, http.StatusCreated, map[string]any{"environment": e})
}

// ---------------------------------------------------------------- secrets (metadata only)

func (h *Handler) list(w http.ResponseWriter, r *http.Request, env string) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	if env != "all" && !validEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	_ = h.Store.EnsureDefaultEnvironments(r.Context(), orgID, projectID)
	list, err := h.Store.ListSecrets(r.Context(), orgID, projectID, env)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list secrets")
		return
	}
	for i := range list {
		list[i].Value = "" // plaintext never leaves through list endpoints
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"secrets": list, "environment": env})
}

func (h *Handler) ListSecrets(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, strings.ToLower(r.PathValue("env")))
}

func (h *Handler) ListAllSecrets(w http.ResponseWriter, r *http.Request) { h.list(w, r, "all") }

func checkValue(w http.ResponseWriter, v string) bool {
	if v == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "value required")
		return false
	}
	if len(v) > MaxValueBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "value exceeds 64KB")
		return false
	}
	return true
}

func (h *Handler) CreateSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	env := strings.ToLower(r.PathValue("env"))
	if !validSecretEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := httpx.DecodeLimit(r, &req, maxBody); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		return
	}
	if !validName(req.Name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name must match ^[A-Za-z_][A-Za-z0-9_]{0,127}$")
		return
	}
	if !checkValue(w, req.Value) {
		return
	}
	meta, err := h.create(r, orgID, projectID, env, req.Name, req.Value, uid)
	if errors.Is(err, store.ErrConflict) {
		httpx.Error(w, http.StatusConflict, "conflict", "secret already exists; use PUT to rotate")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create secret")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"secret": meta})
}

func (h *Handler) create(r *http.Request, orgID, projectID, env, name, value, actor string) (*store.SecretMeta, error) {
	ct, nonce, err := h.Crypto.Encrypt([]byte(value))
	if err != nil {
		return nil, err
	}
	hint := crypto.Hint(value)
	meta, err := h.Store.CreateSecret(r.Context(), orgID, projectID, env, name, hint, ct, nonce, h.Crypto.KeyVersion, actor)
	if err != nil {
		return nil, err
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.create", actor, h.rid(r), map[string]any{"version": 1})
	h.publish(r, actor, orgID, "secret.created", map[string]any{"project_id": projectID, "environment": env, "name": name, "version": 1})
	return meta, nil
}

// GetSecret returns metadata for members; admin+ also receives the plaintext and an
// audit row (secret.reveal) is written for every reveal.
func (h *Handler) GetSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, acc, ok := h.authorizeRole(w, r, membership.LevelView)
	if !ok {
		return
	}
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	revealed := false
	if acc.CanRevealSecrets() {
		ct, nonce, kv, err := h.Store.GetCiphertext(r.Context(), meta.ID, meta.CurrentVersion)
		if err == nil {
			if plain, derr := h.Crypto.DecryptVersion(ct, nonce, kv); derr == nil {
				meta.Value = string(plain)
				revealed = true
			}
		}
	}
	if revealed {
		h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.reveal", uid, h.rid(r), map[string]any{"version": meta.CurrentVersion})
	} else {
		meta.Value = ""
	}
	versions, _ := h.Store.ListVersions(r.Context(), meta.ID)
	httpx.JSON(w, http.StatusOK, map[string]any{"secret": meta, "versions": versions, "revealed": revealed})
}

func (h *Handler) SetSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	var req struct {
		Name    string `json:"name"`
		NewName string `json:"new_name"`
		Value   string `json:"value"`
	}
	if err := httpx.DecodeLimit(r, &req, maxBody); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		return
	}
	target := name
	if t := strings.TrimSpace(req.NewName); t != "" {
		target = t
	} else if t := strings.TrimSpace(req.Name); t != "" {
		target = t
	}
	if !validName(target) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name must match ^[A-Za-z_][A-Za-z0-9_]{0,127}$")
		return
	}
	if len(req.Value) > MaxValueBytes {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "value exceeds 64KB")
		return
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if errors.Is(err, store.ErrNotFound) {
		if !checkValue(w, req.Value) {
			return
		}
		created, cerr := h.create(r, orgID, projectID, env, target, req.Value, uid)
		if errors.Is(cerr, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "a secret with that name already exists")
			return
		}
		if cerr != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to set secret")
			return
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"secret": created})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	var hint string
	var ct, nonce []byte
	if req.Value != "" {
		hint = crypto.Hint(req.Value)
		if ct, nonce, err = h.Crypto.Encrypt([]byte(req.Value)); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "encrypt failed")
			return
		}
	}
	meta, err = h.Store.UpdateSecretWithRename(r.Context(), meta, target, hint, ct, nonce, h.Crypto.KeyVersion, uid)
	if errors.Is(err, store.ErrConflict) {
		httpx.Error(w, http.StatusConflict, "conflict", "a secret with that name already exists")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update secret")
		return
	}
	meta.Value = ""
	h.Store.Audit(r.Context(), orgID, projectID, env, target, "secret.rotate", uid, h.rid(r), map[string]any{"version": meta.CurrentVersion, "old_name": name})
	h.publish(r, uid, orgID, "secret.rotated", map[string]any{"project_id": projectID, "environment": env, "name": target, "version": meta.CurrentVersion})
	httpx.JSON(w, http.StatusOK, map[string]any{"secret": meta})
}

func (h *Handler) DeleteSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	if err := h.Store.DeleteSecret(r.Context(), orgID, projectID, env, name); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.delete", uid, h.rid(r), nil)
	h.publish(r, uid, orgID, "secret.deleted", map[string]any{"project_id": projectID, "environment": env, "name": name})
	w.WriteHeader(http.StatusNoContent)
}

// ListAudit is admin+ and paged (?limit=1..200, ?offset=).
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	limit, offset := 50, 0
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > 200 {
		limit = 200
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	list, err := h.Store.ListAudit(r.Context(), orgID, projectID, limit, offset)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list audit")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"audit": list, "limit": limit, "offset": offset})
}

// ReencryptProject re-encrypts the project's rows still under an old key version (admin+).
func (h *Handler) ReencryptProject(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	n, err := h.reencrypt(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "re-encryption failed after "+strconv.Itoa(n)+" rows")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, "", "", "secret.reencrypt", uid, h.rid(r), map[string]any{"rows": n})
	httpx.JSON(w, http.StatusOK, map[string]any{"reencrypted": n, "key_version": h.Crypto.KeyVersion})
}

func (h *Handler) reencrypt(ctx context.Context, projectID string) (int, error) {
	total := 0
	for {
		rows, err := h.Store.OutdatedVersions(ctx, h.Crypto.KeyVersion, 200, projectID)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			return total, nil
		}
		for _, row := range rows {
			plain, err := h.Crypto.DecryptVersion(row.Ciphertext, row.Nonce, row.KeyVersion)
			if err != nil {
				return total, err
			}
			ct, nonce, err := h.Crypto.Encrypt(plain)
			if err != nil {
				return total, err
			}
			if err := h.Store.SetCiphertext(ctx, row.ID, ct, nonce, h.Crypto.KeyVersion); err != nil {
				return total, err
			}
			total++
		}
	}
}

// ---------------------------------------------------------------- internal

// InternalEnv builds the merged environment for a deployment: all < requested environment.
// Preview deployments only ever receive preview (+all) secrets, never production.
func (h *Handler) InternalEnv(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	env := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("environment")))
	if env == "" {
		env = "production"
	}
	if !validEnv(env) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment")
		return
	}
	rows, err := h.Store.EnvRows(r.Context(), orgID, projectID, []string{"all", env})
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	out := map[string]string{}
	// apply "all" first, then the specific environment so it wins
	for _, pass := range []string{"all", env} {
		for _, row := range rows {
			if row.Environment != pass {
				continue
			}
			plain, err := h.Crypto.DecryptVersion(row.Ciphertext, row.Nonce, row.KeyVersion)
			if err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", "decrypt failed for "+row.Name)
				return
			}
			out[row.Name] = string(plain)
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"env": out, "environment": env})
}

func (h *Handler) InternalValue(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "secret not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	ct, nonce, kv, err := h.Store.GetCiphertext(r.Context(), meta.ID, meta.CurrentVersion)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "version missing")
		return
	}
	plain, err := h.Crypto.DecryptVersion(ct, nonce, kv)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "decrypt failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"name": name, "environment": env, "version": meta.CurrentVersion, "value": string(plain)})
}

// InternalUpsert lets platform services (add-ons, databases) publish connection strings.
func (h *Handler) InternalUpsert(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	var req struct {
		Value string `json:"value"`
		Actor string `json:"actor"`
	}
	if err := httpx.DecodeLimit(r, &req, maxBody); err != nil || !checkValue(w, req.Value) {
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid payload")
		}
		return
	}
	actor := req.Actor
	if actor == "" {
		actor = "service"
	}
	if len(actor) > 40 {
		actor = actor[:40]
	}
	meta, err := h.Store.GetSecret(r.Context(), orgID, projectID, env, name)
	if errors.Is(err, store.ErrNotFound) {
		created, cerr := h.create(r, orgID, projectID, env, name, req.Value, actor)
		if cerr != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create secret")
			return
		}
		httpx.JSON(w, http.StatusCreated, map[string]any{"secret": created})
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	ct, nonce, err := h.Crypto.Encrypt([]byte(req.Value))
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "encrypt failed")
		return
	}
	meta, err = h.Store.UpdateSecretWithRename(r.Context(), meta, name, crypto.Hint(req.Value), ct, nonce, h.Crypto.KeyVersion, actor)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to update secret")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.rotate", actor, h.rid(r), map[string]any{"version": meta.CurrentVersion})
	httpx.JSON(w, http.StatusOK, map[string]any{"secret": meta})
}

// InternalDeleteSecret lets platform services roll back a secret they published (add-on provisioning failed).
func (h *Handler) InternalDeleteSecret(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	env, name := strings.ToLower(r.PathValue("env")), r.PathValue("name")
	if !validSecretEnv(env) || !validName(name) {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid environment or name")
		return
	}
	if err := h.Store.DeleteSecret(r.Context(), orgID, projectID, env, name); errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent) // already gone: idempotent
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.Store.Audit(r.Context(), orgID, projectID, env, name, "secret.delete", "service", h.rid(r), nil)
	w.WriteHeader(http.StatusNoContent)
}

// InternalRotate re-encrypts every row still under an older key version with the current key.
func (h *Handler) InternalRotate(w http.ResponseWriter, r *http.Request) {
	n, err := h.reencrypt(r.Context(), "")
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "re-encryption stopped after "+strconv.Itoa(n)+" rows")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"reencrypted": n, "key_version": h.Crypto.KeyVersion})
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	pid := r.PathValue("projectId")
	if _, err := uuid.Parse(pid); err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	n, err := h.Store.DeleteProject(r.Context(), pid)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "cleanup failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"secrets_removed": n})
}

func (h *Handler) publish(r *http.Request, actor, org, typ string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicSecret, typ, actor, org, payload)
	_, _ = events.PublishJSON(r.Context(), h.Redis, events.TopicSecret, env)
}
