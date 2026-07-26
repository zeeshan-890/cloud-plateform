package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/logging/internal/store"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	BuildURL        string
	HTTP            *http.Client
	LokiURL         string // optional; when set, also push lines to Loki
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(h.Ingest)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/logs/ingest", auth(http.HandlerFunc(h.Ingest)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(h.Query)))

	mux.HandleFunc("POST /internal/logs", h.InternalIngest)
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

func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
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
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid body")
		return
	}
	lines := req.Lines
	if req.Message != "" {
		lines = append(lines, req.Message)
	}
	if len(lines) == 0 {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "message or lines required")
		return
	}
	var created []store.Entry
	for _, line := range lines {
		e := &store.Entry{
			OrgID: orgID, ProjectID: projectID,
			Source: req.Source, Level: req.Level, Message: line,
			InstanceID: req.InstanceID, RequestID: middleware.RequestIDFrom(r.Context()), Attrs: req.Attrs,
		}
		if req.BuildID != "" {
			e.BuildID = &req.BuildID
		}
		if req.DeploymentID != "" {
			e.DeploymentID = &req.DeploymentID
		}
		if req.LoggedAt != nil {
			e.LoggedAt = *req.LoggedAt
		}
		if err := h.Store.Ingest(r.Context(), e); err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
			return
		}
		h.pushLoki(e)
		created = append(created, *e)
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"ingested": len(created), "entries": created})
}

func (h *Handler) InternalIngest(w http.ResponseWriter, r *http.Request) {
	var req store.Entry
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" || req.Message == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "org_id, project_id, message required")
		return
	}
	if err := h.Store.Ingest(r.Context(), &req); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	h.pushLoki(&req)
	httpx.JSON(w, http.StatusCreated, map[string]any{"entry": req})
}

func (h *Handler) Query(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	q := store.Query{
		Source:  r.URL.Query().Get("source"),
		Level:   r.URL.Query().Get("level"),
		BuildID: r.URL.Query().Get("build_id"),
		Q:       r.URL.Query().Get("q"),
	}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil {
			q.Limit = n
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			q.Since = &t
		}
	}

	entries, err := h.Store.Query(r.Context(), orgID, projectID, q)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "query failed")
		return
	}

	// Optionally merge build logs from build service when source=build and build_id set
	var buildLogs string
	if q.Source == "build" && q.BuildID != "" && h.BuildURL != "" {
		buildLogs = h.fetchBuildLogs(r, orgID, projectID, q.BuildID)
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"entries":    entries,
		"build_logs": buildLogs,
		"backend":    h.backendLabel(),
	})
}

func (h *Handler) backendLabel() string {
	if h.LokiURL != "" {
		return "postgres+loki"
	}
	return "postgres"
}

func (h *Handler) fetchBuildLogs(r *http.Request, orgID, projectID, buildID string) string {
	url := fmt.Sprintf("%s/orgs/%s/projects/%s/builds/%s/logs",
		strings.TrimRight(h.BuildURL, "/"), orgID, projectID, buildID)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := h.HTTP.Do(req)
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
	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &out)
	return out.Logs
}

func (h *Handler) pushLoki(e *store.Entry) {
	if h.LokiURL == "" || h.HTTP == nil {
		return
	}
	ts := strconv.FormatInt(e.LoggedAt.UnixNano(), 10)
	stream := map[string]string{
		"job": "jp", "org_id": e.OrgID, "project_id": e.ProjectID,
		"source": e.Source, "level": e.Level,
	}
	payload := map[string]any{
		"streams": []map[string]any{
			{"stream": stream, "values": [][]string{{ts, e.Message}}},
		},
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(h.LokiURL, "/")+"/loki/api/v1/push", strings.NewReader(string(b)))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
}
