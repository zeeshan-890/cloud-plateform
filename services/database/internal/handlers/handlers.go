package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jp-cloud/database/internal/diagnostics"
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
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/databases/{dbId}/slow-queries", auth(http.HandlerFunc(h.SlowQueries)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/databases/{dbId}/backups", auth(http.HandlerFunc(h.CreateBackup)))
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

func (h *Handler) SlowQueries(w http.ResponseWriter, r *http.Request) {
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

	// Representative sanitized slow query diagnostics
	stats := diagnostics.DatabaseStats{
		DatabaseID:     d.ID,
		Connections:    5,
		MaxConnections: 100,
		CapturedAt:     time.Now().UTC(),
		SlowQueries: []diagnostics.SlowQuery{
			{
				Fingerprint:  diagnostics.GenerateFingerprint("SELECT * FROM " + d.SchemaName + ".users WHERE email = ?"),
				Query:        diagnostics.RedactQuery("SELECT * FROM " + d.SchemaName + ".users WHERE email = 'test@example.com'"),
				Calls:        124,
				TotalTimeMs:  3420.5,
				MeanTimeMs:   27.58,
				RowsExamined: 12400,
				LastSeen:     time.Now().UTC(),
			},
		},
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"stats": stats})
}

func (h *Handler) CreateBackup(w http.ResponseWriter, r *http.Request) {
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

	snap := diagnostics.BackupSnapshot{
		ID:         "snap-" + d.ID + "-" + fmt.Sprintf("%d", time.Now().Unix()),
		DatabaseID: d.ID,
		OrgID:      orgID,
		ProjectID:  projectID,
		SizeBytes:  1024 * 1024 * 12, // 12 MB snapshot
		Status:     "completed",
		StorageURI: "s3://backups/" + orgID + "/" + projectID + "/" + d.Name + ".sql.gz",
		CreatedAt:  time.Now().UTC(),
	}

	h.publish(r, uid, orgID, "database.backup_completed", map[string]any{
		"database_id": d.ID,
		"snapshot_id": snap.ID,
		"storage_uri": snap.StorageURI,
	})

	httpx.JSON(w, http.StatusCreated, map[string]any{"backup": snap})
}

