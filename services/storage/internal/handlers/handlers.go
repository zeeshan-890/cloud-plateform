package handlers

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/storage/internal/miniox"
	"github.com/jp-cloud/storage/internal/store"
	"github.com/redis/go-redis/v9"
)

// ReservedPrefix is written only by platform services (add-on / database backups).
const ReservedPrefix = "_backups/"

// Authz is implemented by *membership.Client.
type Authz interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

type Handler struct {
	Store   store.Store
	Backend miniox.Backend
	JWT     *jwtutil.Manager
	Authz   Authz
	Redis   *redis.Client
	Log     *slog.Logger

	MaxJSONUpload int64  // decoded bytes accepted by the base64 JSON endpoint (default 50 MiB)
	MaxObject     int64  // streaming upload ceiling (default 2 GiB)
	QuotaBytes    int64  // per project, 0 = unlimited
	PublicURL     string // STORAGE_PUBLIC_URL: reachable MinIO base used for presigned URLs
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) maxJSON() int64 {
	if h.MaxJSONUpload > 0 {
		return h.MaxJSONUpload
	}
	return 16 << 20
}

func (h *Handler) maxObject() int64 {
	if h.MaxObject > 0 {
		return h.MaxObject
	}
	return 2 << 30
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	p := "/orgs/{orgId}/projects/{projectId}/storage"
	mux.Handle("GET "+p+"/bucket", auth(http.HandlerFunc(h.GetBucket)))
	mux.Handle("POST "+p+"/bucket", auth(http.HandlerFunc(h.EnsureBucket)))
	mux.Handle("DELETE "+p+"/bucket", auth(http.HandlerFunc(h.DeleteBucket)))
	mux.Handle("GET "+p+"/usage", auth(http.HandlerFunc(h.Usage)))
	mux.Handle("GET "+p+"/objects", auth(http.HandlerFunc(h.ListObjects)))
	mux.Handle("POST "+p+"/objects", auth(http.HandlerFunc(h.UploadJSON)))
	mux.Handle("PUT "+p+"/objects", auth(http.HandlerFunc(h.UploadStream)))
	mux.Handle("DELETE "+p+"/objects", auth(http.HandlerFunc(h.DeleteObject)))
	mux.Handle("GET "+p+"/download", auth(http.HandlerFunc(h.Download)))
	mux.Handle("POST "+p+"/signed-url", auth(http.HandlerFunc(h.SignedURL)))

	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("PUT /internal/projects/{projectId}/objects", in(h.InternalPut))
	mux.Handle("GET /internal/projects/{projectId}/objects", in(h.InternalGet))
	mux.Handle("DELETE /internal/projects/{projectId}/objects", in(h.InternalDeleteObject))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

// ------------------------------------------------------------------ key validation

// ValidateKey enforces object key rules (no traversal, control characters, absurd length).
func ValidateKey(key string) error {
	switch {
	case key == "":
		return errors.New("key is required")
	case len(key) > 1024:
		return errors.New("key longer than 1024 bytes")
	case !utf8.ValidString(key):
		return errors.New("key must be valid UTF-8")
	case strings.HasPrefix(key, "/"):
		return errors.New("key must not start with '/'")
	case strings.Contains(key, "\\"):
		return errors.New("key must not contain backslashes")
	case strings.Contains(key, "//"):
		return errors.New("key must not contain empty path segments")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || r == 0xfffd || r == 0x2028 || r == 0x2029 {
			return errors.New("key must not contain control characters")
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." || seg == "." {
			return errors.New("key must not contain '.' or '..' segments")
		}
	}
	return nil
}

func isActiveContent(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.HasPrefix(ct, "text/html") || strings.Contains(ct, "svg") || strings.Contains(ct, "javascript") || strings.Contains(ct, "xml") || strings.HasPrefix(ct, "text/xsl")
}

// sniff decides the stored content type: a specific client type wins, otherwise the content is sniffed.
func sniff(declared string, br *bufio.Reader) string {
	declared = strings.TrimSpace(declared)
	if declared != "" && declared != "application/octet-stream" && !strings.HasPrefix(declared, "multipart/") {
		return declared
	}
	head, _ := br.Peek(512)
	return http.DetectContentType(head)
}

// ------------------------------------------------------------------ helpers

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request, need membership.Level) (orgID, projectID, uid string, ok bool) {
	uid, orgID, projectID = middleware.UserIDFrom(r.Context()), r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, good := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaStorage, need); !good {
		return
	}
	return orgID, projectID, uid, true
}

// bucket returns (and creates on demand) the project bucket in both the DB and the backend.
func (h *Handler) bucket(ctx context.Context, orgID, projectID string) (*store.Bucket, error) {
	b, err := h.Store.EnsureBucket(ctx, orgID, projectID)
	if err != nil {
		return nil, err
	}
	if err := h.Backend.EnsureBucket(ctx, b.Name); err != nil {
		return nil, fmt.Errorf("object store: %w", err)
	}
	return b, nil
}

type usageView struct {
	UsedBytes  int64 `json:"used_bytes"`
	Objects    int64 `json:"objects"`
	QuotaBytes int64 `json:"quota_bytes"`
}

func (h *Handler) usage(ctx context.Context, projectID string) usageView {
	b, c, _ := h.Store.Usage(ctx, projectID)
	return usageView{UsedBytes: b, Objects: c, QuotaBytes: h.QuotaBytes}
}

func (h *Handler) GetBucket(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	b, err := h.Store.GetBucket(r.Context(), projectID)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "bucket not created yet")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bucket": b, "backend": h.Backend.Name(), "usage": h.usage(r.Context(), projectID)})
}

func (h *Handler) EnsureBucket(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	b, err := h.bucket(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"bucket": b, "backend": h.Backend.Name(), "usage": h.usage(r.Context(), projectID)})
}

func (h *Handler) DeleteBucket(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	if r.URL.Query().Get("confirm") != "true" {
		httpx.Error(w, http.StatusBadRequest, "confirmation_required", "pass ?confirm=true to delete the bucket and all its objects")
		return
	}
	if err := h.removeProject(r.Context(), projectID); err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	h.publish(r.Context(), events.TypeStorageDeleted, uid, orgID, map[string]any{"project_id": projectID, "bucket": true})
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) removeProject(ctx context.Context, projectID string) error {
	b, err := h.Store.GetBucket(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := h.Backend.DeleteBucket(ctx, b.Name); err != nil {
		return fmt.Errorf("object store: %w", err)
	}
	return h.Store.DeleteBucket(ctx, projectID)
}

func (h *Handler) Usage(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"usage": h.usage(r.Context(), projectID)})
}

func (h *Handler) ListObjects(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "limit must be 1..1000")
			return
		}
		limit = n
	}
	prefix := q.Get("prefix")
	if len(prefix) > 1024 || !utf8.ValidString(prefix) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid prefix")
		return
	}
	list, err := h.Store.ListObjects(r.Context(), projectID, prefix, q.Get("cursor"), limit+1)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	next := ""
	if len(list) > limit {
		list = list[:limit]
		next = list[limit-1].Key
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"objects": list, "next_cursor": next, "usage": h.usage(r.Context(), projectID)})
}

// store streams body into the backend and records it; shared by every upload path.
func (h *Handler) put(ctx context.Context, b *store.Bucket, key, declaredCT string, body io.Reader, size int64, limit int64) (*store.Object, *apiErr) {
	if err := ValidateKey(key); err != nil {
		return nil, &apiErr{400, "invalid_key", err.Error()}
	}
	used, _, _ := h.Store.Usage(ctx, b.ProjectID)
	var prevSize int64
	if old, err := h.Store.GetObject(ctx, b.ProjectID, key); err == nil {
		prevSize = old.SizeBytes
	}
	max := limit
	if h.QuotaBytes > 0 {
		remaining := h.QuotaBytes - used + prevSize
		if remaining <= 0 || (size >= 0 && size > remaining) {
			return nil, &apiErr{413, "quota_exceeded", fmt.Sprintf("project storage quota of %d bytes exceeded", h.QuotaBytes)}
		}
		if remaining < max {
			max = remaining
		}
	}
	if size > max {
		return nil, &apiErr{413, "too_large", fmt.Sprintf("object exceeds the %d byte limit", max)}
	}
	br := bufio.NewReaderSize(io.LimitReader(body, max+1), 4096)
	ct := sniff(declaredCT, br)
	counter := &countingReader{r: br}
	etag, stored, err := h.Backend.Put(ctx, b.Name, key, ct, counter, size)
	if err != nil {
		return nil, &apiErr{502, "storage_error", err.Error()}
	}
	if counter.n > max {
		_ = h.Backend.Delete(ctx, b.Name, key)
		code := "too_large"
		if h.QuotaBytes > 0 && max < limit {
			code = "quota_exceeded"
		}
		return nil, &apiErr{413, code, "upload exceeds the allowed size"}
	}
	obj, _, err := h.Store.UpsertObject(ctx, b, key, ct, etag, stored)
	if err != nil {
		_ = h.Backend.Delete(ctx, b.Name, key)
		return nil, &apiErr{500, "internal", "could not record object"}
	}
	return obj, nil
}

type apiErr struct {
	status        int
	code, message string
}

func (e *apiErr) write(w http.ResponseWriter) { httpx.Error(w, e.status, e.code, e.message) }

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// UploadJSON accepts {"key","content_base64","content_type"} up to STORAGE_MAX_UPLOAD.
func (h *Handler) UploadJSON(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	var req struct {
		Key           string `json:"key"`
		ContentBase64 string `json:"content_base64"`
		ContentType   string `json:"content_type"`
	}
	if err := httpx.DecodeLimit(r, &req, h.maxJSON()*4/3+4096); err != nil {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", "invalid or too large request body")
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.ContentBase64)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "content_base64 is not valid base64")
		return
	}
	if strings.HasPrefix(req.Key, ReservedPrefix) {
		httpx.Error(w, http.StatusForbidden, "reserved_key", "keys under "+ReservedPrefix+" are reserved")
		return
	}
	b, err := h.bucket(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	obj, e := h.put(r.Context(), b, req.Key, req.ContentType, strings.NewReader(string(data)), int64(len(data)), h.maxJSON())
	if e != nil {
		e.write(w)
		return
	}
	h.publish(r.Context(), events.TypeStorageUploaded, uid, orgID, map[string]any{"project_id": projectID, "key": obj.Key, "size": obj.SizeBytes})
	httpx.JSON(w, http.StatusCreated, map[string]any{"object": obj})
}

// UploadStream: PUT ...?key= streams the request body straight to the object store.
func (h *Handler) UploadStream(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	key := r.URL.Query().Get("key")
	if strings.HasPrefix(key, ReservedPrefix) {
		httpx.Error(w, http.StatusForbidden, "reserved_key", "keys under "+ReservedPrefix+" are reserved")
		return
	}
	b, err := h.bucket(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	size := r.ContentLength // -1 when chunked
	obj, e := h.put(r.Context(), b, key, r.Header.Get("Content-Type"), http.MaxBytesReader(w, r.Body, h.maxObject()+1), size, h.maxObject())
	if e != nil {
		e.write(w)
		return
	}
	h.publish(r.Context(), events.TypeStorageUploaded, uid, orgID, map[string]any{"project_id": projectID, "key": obj.Key, "size": obj.SizeBytes})
	httpx.JSON(w, http.StatusCreated, map[string]any{"object": obj})
}

// Download proxies the object with the caller's authorization.
func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	_, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	h.serve(w, r, projectID, r.URL.Query().Get("key"))
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request, projectID, key string) {
	if err := ValidateKey(key); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_key", err.Error())
		return
	}
	obj, err := h.Store.GetObject(r.Context(), projectID, key)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "object not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	b, err := h.Store.GetBucket(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "bucket not found")
		return
	}
	rc, info, err := h.Backend.Get(r.Context(), b.Name, key)
	if errors.Is(err, miniox.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "object missing in the object store")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	defer rc.Close()
	ct := obj.ContentType
	if ct == "" {
		ct = info.ContentType
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size, 10))
	name := key[strings.LastIndexByte(key, '/')+1:]
	disp := "inline"
	if isActiveContent(ct) || r.URL.Query().Get("download") == "true" {
		disp = "attachment" // never render stored HTML/SVG/JS on the platform origin
	}
	w.Header().Set("Content-Disposition", disp+"; filename*=UTF-8''"+urlEscape(name))
	w.Header().Set("ETag", `"`+strings.Trim(obj.ETag, `"`)+`"`)
	if _, err := io.Copy(w, rc); err != nil {
		h.log().Warn("download interrupted", "key", key, "err", err)
	}
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// SignedURL returns an API download URL (always valid) and, when STORAGE_PUBLIC_URL is configured,
// a MinIO presigned URL for that public host.
func (h *Handler) SignedURL(w http.ResponseWriter, r *http.Request) {
	orgID, projectID, _, ok := h.authorize(w, r, membership.LevelView)
	if !ok {
		return
	}
	var req struct {
		Key     string `json:"key"`
		Expires int    `json:"expires_in"`
	}
	_ = httpx.Decode(r, &req)
	key := firstNonEmpty(req.Key, r.URL.Query().Get("key"))
	if err := ValidateKey(key); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_key", err.Error())
		return
	}
	if _, err := h.Store.GetObject(r.Context(), projectID, key); err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "object not found")
		return
	}
	exp := time.Duration(req.Expires) * time.Second
	if exp <= 0 {
		exp = 15 * time.Minute
	}
	if exp > 24*time.Hour {
		exp = 24 * time.Hour
	}
	out := map[string]any{
		"download_path": fmt.Sprintf("/api/v1/orgs/%s/projects/%s/storage/download?key=%s", orgID, projectID, urlQuery(key)),
		"mode":          "proxy",
	}
	if h.PublicURL != "" {
		b, err := h.Store.GetBucket(r.Context(), projectID)
		if err == nil {
			if u, err := h.Backend.SignedURL(r.Context(), b.Name, key, exp, h.PublicURL); err == nil {
				out["url"], out["mode"], out["expires_in"] = u, "presigned", int(exp.Seconds())
			}
		}
	}
	httpx.JSON(w, http.StatusOK, out)
}

func urlQuery(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '/' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// DeleteObject: ?key= (member+) or ?prefix= bulk (admin+).
func (h *Handler) DeleteObject(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	orgID, projectID, uid, ok := h.authorize(w, r, membership.LevelManage)
	if !ok {
		return
	}
	b, err := h.Store.GetBucket(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, http.StatusNotFound, "not_found", "bucket not found")
		return
	}
	if prefix := q.Get("prefix"); prefix != "" {
		if len(prefix) > 1024 || strings.HasPrefix(prefix, ReservedPrefix) || strings.HasPrefix(ReservedPrefix, prefix) {
			httpx.Error(w, http.StatusForbidden, "reserved_key", "that prefix covers reserved keys")
			return
		}
		keys, err := h.Store.DeleteByPrefix(r.Context(), projectID, prefix)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
			return
		}
		for _, k := range keys {
			if err := h.Backend.Delete(r.Context(), b.Name, k); err != nil {
				h.log().Error("backend delete", "key", k, "err", err)
			}
		}
		h.publish(r.Context(), events.TypeStorageDeleted, uid, orgID, map[string]any{"project_id": projectID, "prefix": prefix, "count": len(keys)})
		httpx.JSON(w, http.StatusOK, map[string]any{"deleted": len(keys)})
		return
	}
	key := q.Get("key")
	if err := ValidateKey(key); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_key", err.Error())
		return
	}
	if strings.HasPrefix(key, ReservedPrefix) {
		httpx.Error(w, http.StatusForbidden, "reserved_key", "keys under "+ReservedPrefix+" are reserved")
		return
	}
	if err := h.Store.DeleteObject(r.Context(), projectID, key); errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "object not found")
		return
	} else if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	if err := h.Backend.Delete(r.Context(), b.Name, key); err != nil {
		h.log().Error("backend delete", "key", key, "err", err)
	}
	h.publish(r.Context(), events.TypeStorageDeleted, uid, orgID, map[string]any{"project_id": projectID, "key": key})
	w.WriteHeader(http.StatusNoContent)
}

// ------------------------------------------------------------------ internal API

func (h *Handler) InternalPut(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	orgID := r.URL.Query().Get("org_id")
	if !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	if !validID(orgID) {
		if b, err := h.Store.GetBucket(r.Context(), projectID); err == nil {
			orgID = b.OrgID
		} else {
			httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id required when the bucket does not exist yet")
			return
		}
	}
	b, err := h.bucket(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusBadGateway, "storage_error", err.Error())
		return
	}
	obj, e := h.put(r.Context(), b, r.URL.Query().Get("key"), r.Header.Get("Content-Type"), r.Body, r.ContentLength, h.maxObject())
	if e != nil {
		e.write(w)
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"object": obj})
}

func (h *Handler) InternalGet(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	h.serve(w, r, projectID, r.URL.Query().Get("key"))
}

func (h *Handler) InternalDeleteObject(w http.ResponseWriter, r *http.Request) {
	projectID, key := r.PathValue("projectId"), r.URL.Query().Get("key")
	if !validID(projectID) || ValidateKey(key) != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id or key")
		return
	}
	if b, err := h.Store.GetBucket(r.Context(), projectID); err == nil {
		_ = h.Backend.Delete(r.Context(), b.Name, key)
	}
	_ = h.Store.DeleteObject(r.Context(), projectID, key)
	w.WriteHeader(http.StatusNoContent)
}

// InternalDeleteProject removes the bucket and all objects (project delete cascade).
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	if err := h.removeProject(r.Context(), projectID); err != nil {
		httpx.Error(w, http.StatusBadGateway, "partial_failure", err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project_id": projectID, "removed": true})
}

func (h *Handler) publish(ctx context.Context, typ, actor, org string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicStorage, typ, actor, org, payload)
	if _, err := events.PublishJSON(ctx, h.Redis, events.TopicStorage, env); err != nil {
		h.log().Warn("publish event", "type", typ, "err", err)
	}
}
