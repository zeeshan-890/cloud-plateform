package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jp-cloud/database/internal/provisioner"
	"github.com/jp-cloud/database/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	Provisioner     *provisioner.Provisioner
	JWT             *jwtutil.Manager
	OrganizationURL string
	SecretURL       string
	Redis           *redis.Client
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/databases", auth(http.HandlerFunc(h.List)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/databases", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/databases/{dbId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/databases/{dbId}", auth(http.HandlerFunc(h.Delete)))
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
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"databases": list, "mode": h.Provisioner.Mode})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Name string `json:"name"`
		Env  string `json:"env"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required")
		return
	}
	env := strings.ToLower(strings.TrimSpace(req.Env))
	if env == "" {
		env = "development"
	}
	res, err := h.Provisioner.Create(r.Context(), orgID, projectID, req.Name)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "provision_failed", err.Error())
		return
	}
	secretName := "DATABASE_URL_" + strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(req.Name), "-", "_"))
	secretRef := h.storeSecret(r, orgID, projectID, env, secretName, res.ConnectionURL)
	d := &store.Database{
		OrgID: orgID, ProjectID: projectID, Name: strings.TrimSpace(req.Name),
		Mode: res.Mode, Status: "ready", SchemaName: res.SchemaName, RoleName: res.RoleName,
		SecretRef: secretRef, ConnectionHint: res.ConnectionHint, CreatedBy: &uid,
	}
	if err := h.Store.Create(r.Context(), d); err == store.ErrConflict {
		_ = h.Provisioner.Drop(r.Context(), res.SchemaName, res.RoleName)
		httpx.Error(w, http.StatusConflict, "conflict", "database name already exists")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	h.publish(r, uid, orgID, events.TypeDatabaseCreated, map[string]any{
		"project_id": projectID, "database_id": d.ID, "name": d.Name, "mode": d.Mode, "secret_ref": d.SecretRef,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"database": d})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	dbID := r.PathValue("dbId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, dbID)
	if err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "database not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "get failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"database": d})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	dbID := r.PathValue("dbId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	d, err := h.Store.Delete(r.Context(), orgID, projectID, dbID)
	if err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "database not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	_ = h.Provisioner.Drop(r.Context(), d.SchemaName, d.RoleName)
	h.publish(r, uid, orgID, events.TypeDatabaseDeleted, map[string]any{
		"project_id": projectID, "database_id": d.ID, "name": d.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) storeSecret(r *http.Request, orgID, projectID, env, name, value string) string {
	ref := fmt.Sprintf("secret://%s/%s/%s/%s", orgID, projectID, env, name)
	secretURL := h.SecretURL
	if secretURL == "" {
		secretURL = os.Getenv("SECRET_URL")
	}
	if secretURL == "" || h.HTTP == nil {
		return ref
	}
	body, _ := json.Marshal(map[string]string{"value": value})
	url := fmt.Sprintf("%s/orgs/%s/projects/%s/environments/%s/secrets/%s",
		strings.TrimRight(secretURL, "/"), orgID, projectID, env, name)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return ref
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return ref
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return ref
}

func (h *Handler) publish(r *http.Request, actor, org, typ string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDatabase, typ, actor, org, payload)
	_, _ = events.PublishJSON(r.Context(), h.Redis, events.TopicDatabase, env)
}
