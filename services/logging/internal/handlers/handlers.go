package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/logging/internal/store"
)

const (
	maxLines      = 500
	maxMessageLen = 16 << 10
	maxAttrsBytes = 8 << 10
)

type Handler struct {
	Store    store.Repo
	JWT      *jwtutil.Manager
	Members  *membership.Client
	BuildURL string
	HTTP     *http.Client // plain client: forwards the caller's JWT to the build service
	LokiURL  string       // optional; when set, ingested lines are also pushed to Loki
	Log      *slog.Logger
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(h.Ingest)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/logs/ingest", auth(http.HandlerFunc(h.Ingest)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(h.Query)))

	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/logs", in(h.InternalIngest))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

// Ingest (logs/metrics manage) stores log lines for a project.
func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaLogs, membership.LevelManage); !ok {
		return
	}
	var req struct {
		Source       string         `json:"source"`
		Level        string         `json:"level"`
		Message      string         `json:"message"`
		Lines        []string       `json:"lines"`
		BuildID      string         `json:"build_id"`
		InstanceID   string         `json:"instance_id"`
		DeploymentID string         `json:"deployment_id"`
		LoggedAt     *time.Time     `json:"logged_at"`
		Attrs        map[string]any `json:"attrs"`
	}
	if err := httpx.DecodeLimit(r, &req, 4<<20); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid body")
		return
	}
	lines := append([]string(nil), req.Lines...)
	if req.Message != "" {
		lines = append(lines, req.Message)
	}
	if len(lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "message or lines required")
		return
	}
	if len(lines) > maxLines {
		httpx.Error(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("at most %d lines per request", maxLines))
		return
	}
	if b, _ := json.Marshal(req.Attrs); len(b) > maxAttrsBytes {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "attrs too large")
		return
	}
	level := strings.ToLower(req.Level)
	entries := make([]*store.Entry, 0, len(lines))
	for _, line := range lines {
		e := &store.Entry{
			OrgID: orgID, ProjectID: projectID, Source: clip(req.Source, 32), Level: clip(level, 16), Message: clip(line, maxMessageLen),
			InstanceID: clip(req.InstanceID, 128), RequestID: middleware.RequestIDFrom(r.Context()), Attrs: req.Attrs,
		}
		if req.BuildID != "" {
			e.BuildID = &req.BuildID
		}
		if req.DeploymentID != "" {
			e.DeploymentID = &req.DeploymentID
		}
		if req.LoggedAt != nil && req.LoggedAt.Before(time.Now().Add(5*time.Minute)) {
			e.LoggedAt = *req.LoggedAt
		}
		entries = append(entries, e)
	}
	if err := h.Store.IngestBatch(r.Context(), entries); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	h.pushLoki(r.Context(), entries)
	httpx.JSON(w, http.StatusCreated, map[string]any{"ingested": len(entries)})
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// InternalIngest is for platform services (INTERNAL_TOKEN required).
func (h *Handler) InternalIngest(w http.ResponseWriter, r *http.Request) {
	var req store.Entry
	if err := httpx.DecodeLimit(r, &req, 1<<20); err != nil || req.OrgID == "" || req.ProjectID == "" || req.Message == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "org_id, project_id, message required")
		return
	}
	req.Message = clip(req.Message, maxMessageLen)
	if err := h.Store.IngestBatch(r.Context(), []*store.Entry{&req}); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	h.pushLoki(r.Context(), []*store.Entry{&req})
	httpx.JSON(w, http.StatusCreated, map[string]any{"entry": req})
}

// InternalDeleteProject removes every log line of a project (project delete cascade).
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Store.DeleteProject(r.Context(), r.PathValue("projectId")); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Query (view) returns newest-first with cursor pagination (`before`).
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaLogs, membership.LevelView); !ok {
		return
	}
	qp := r.URL.Query()
	q := store.Query{Source: clip(qp.Get("source"), 32), Level: clip(strings.ToLower(qp.Get("level")), 16), BuildID: qp.Get("build_id"), Q: clip(qp.Get("q"), 200)}
	if q.BuildID != "" && len(q.BuildID) != 36 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid build_id")
		return
	}
	q.Limit = store.DefaultLimit
	if n, err := strconv.Atoi(qp.Get("limit")); err == nil && n > 0 {
		q.Limit = n
		if q.Limit > store.MaxLimit {
			q.Limit = store.MaxLimit
		}
	}
	for key, dst := range map[string]**time.Time{"since": &q.Since, "before": &q.Before} {
		if v := qp.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "validation_error", key+" must be RFC3339")
				return
			}
			*dst = &t
		}
	}
	entries, err := h.Store.Query(r.Context(), orgID, projectID, q)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}
	var buildLogs string
	if q.Source == "build" && q.BuildID != "" && h.BuildURL != "" {
		buildLogs = h.fetchBuildLogs(r, orgID, projectID, q.BuildID)
	}
	next := ""
	if len(entries) == q.Limit {
		next = entries[len(entries)-1].LoggedAt.Format(time.RFC3339Nano)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"entries": entries, "build_logs": buildLogs, "backend": h.backendLabel(), "next_before": next})
}

func (h *Handler) backendLabel() string {
	if h.LokiURL != "" {
		return "postgres+loki"
	}
	return "postgres"
}

func (h *Handler) fetchBuildLogs(r *http.Request, orgID, projectID, buildID string) string {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	u := fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs", strings.TrimRight(h.BuildURL, "/"), orgID, projectID, buildID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ""
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var out struct {
		Logs string `json:"logs"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out)
	return out.Logs
}

func (h *Handler) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return http.DefaultClient
}

// pushLoki forwards a batch to Loki in one bounded, synchronous request (no goroutine per line).
func (h *Handler) pushLoki(ctx context.Context, entries []*store.Entry) {
	if h.LokiURL == "" || len(entries) == 0 {
		return
	}
	type stream struct {
		Stream map[string]string `json:"stream"`
		Values [][]string        `json:"values"`
	}
	groups := map[string]*stream{}
	for _, e := range entries {
		key := e.OrgID + "|" + e.ProjectID + "|" + e.Source + "|" + e.Level
		g, ok := groups[key]
		if !ok {
			g = &stream{Stream: map[string]string{"job": "jp", "org_id": e.OrgID, "project_id": e.ProjectID, "source": e.Source, "level": e.Level}}
			groups[key] = g
		}
		g.Values = append(g.Values, []string{strconv.FormatInt(e.LoggedAt.UnixNano(), 10), e.Message})
	}
	streams := make([]*stream, 0, len(groups))
	for _, g := range groups {
		streams = append(streams, g)
	}
	b, _ := json.Marshal(map[string]any{"streams": streams})
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, strings.TrimRight(h.LokiURL, "/")+"/loki/api/v1/push", bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.client().Do(req)
	if err != nil {
		h.log().Warn("loki push failed", "err", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
}
