package handlers

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/storage/internal/miniox"
	"github.com/jp-cloud/storage/internal/store"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	Backend         miniox.Backend
	JWT             *jwtutil.Manager
	OrganizationURL string
	Redis           *redis.Client
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/storage/bucket", auth(http.HandlerFunc(h.GetBucket)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/storage/bucket", auth(http.HandlerFunc(h.EnsureBucket)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(h.ListObjects)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(h.UploadObject)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/storage/signed-url", auth(http.HandlerFunc(h.SignedURL)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(h.DeleteObject)))
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

func (h *Handler) GetBucket(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	b, err := h.Store.EnsureBucket(r.Context(), orgID, projectID, h.Backend.Mode())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to get bucket")
		return
	}
	_ = h.Backend.EnsureBucket(r.Context(), b.Name)
	httpx.JSON(w, http.StatusOK, map[string]any{"bucket": b, "mode": h.Backend.Mode()})
}

func (h *Handler) EnsureBucket(w http.ResponseWriter, r *http.Request) {
	h.GetBucket(w, r)
}

func (h *Handler) ListObjects(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	prefix := r.URL.Query().Get("prefix")
	b, err := h.Store.EnsureBucket(r.Context(), orgID, projectID, h.Backend.Mode())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "bucket failed")
		return
	}
	_ = h.Backend.EnsureBucket(r.Context(), b.Name)
	list, err := h.Store.ListObjects(r.Context(), orgID, projectID, prefix)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"objects": list, "bucket": b.Name, "mode": h.Backend.Mode()})
}

func (h *Handler) UploadObject(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Key         string `json:"key"`
		ContentType string `json:"content_type"`
		DataBase64  string `json:"data_base64"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Key) == "" || req.DataBase64 == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "key and data_base64 required")
		return
	}
	key := strings.TrimLeft(strings.TrimSpace(req.Key), "/")
	ct := req.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	raw, err := base64.StdEncoding.DecodeString(req.DataBase64)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid base64")
		return
	}
	if len(raw) > 5*1024*1024 {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "max 5MB via API upload")
		return
	}
	b, err := h.Store.EnsureBucket(r.Context(), orgID, projectID, h.Backend.Mode())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "bucket failed")
		return
	}
	if err := h.Backend.EnsureBucket(r.Context(), b.Name); err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_unavailable", "object store unavailable")
		return
	}
	etag, size, err := h.Backend.Put(r.Context(), b.Name, key, ct, raw)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "upload_failed", err.Error())
		return
	}
	obj, err := h.Store.UpsertObject(r.Context(), orgID, projectID, b.ID, key, ct, etag, size)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "metadata failed")
		return
	}
	h.publish(r, uid, orgID, events.TypeStorageUploaded, map[string]any{
		"project_id": projectID, "key": key, "size_bytes": size, "bucket": b.Name,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"object": obj, "mode": h.Backend.Mode()})
}

func (h *Handler) SignedURL(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Key     string `json:"key"`
		Expires string `json:"expires"`
	}
	_ = httpx.Decode(r, &req)
	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = strings.TrimSpace(r.URL.Query().Get("key"))
	}
	if key == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "key required")
		return
	}
	b, err := h.Store.EnsureBucket(r.Context(), orgID, projectID, h.Backend.Mode())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "bucket failed")
		return
	}
	expiry := 15 * time.Minute
	expStr := req.Expires
	if expStr == "" {
		expStr = r.URL.Query().Get("expires")
	}
	if expStr != "" {
		if d, err := time.ParseDuration(expStr); err == nil && d > 0 && d <= 24*time.Hour {
			expiry = d
		}
	}
	url, err := h.Backend.SignedURL(r.Context(), b.Name, key, expiry)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "signed_url_failed", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"url": url, "key": key, "expires_in_seconds": int(expiry.Seconds()), "mode": h.Backend.Mode(),
	})
}

func (h *Handler) DeleteObject(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if key == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "key query param required")
		return
	}
	b, err := h.Store.EnsureBucket(r.Context(), orgID, projectID, h.Backend.Mode())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "bucket failed")
		return
	}
	_ = h.Backend.Delete(r.Context(), b.Name, key)
	if err := h.Store.DeleteObject(r.Context(), orgID, projectID, key); err == store.ErrNotFound {
		httpx.Error(w, http.StatusNotFound, "not_found", "object not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	h.publish(r, uid, orgID, events.TypeStorageDeleted, map[string]any{
		"project_id": projectID, "key": key, "bucket": b.Name,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) publish(r *http.Request, actor, org, typ string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicStorage, typ, actor, org, payload)
	_, _ = events.PublishJSON(r.Context(), h.Redis, events.TopicStorage, env)
}
