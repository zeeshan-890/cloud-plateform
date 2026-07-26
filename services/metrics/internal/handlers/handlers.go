package handlers

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"

	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/metrics/internal/store"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	HTTP            *http.Client
	Simulate        bool
	reqCount        atomic.Int64
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.HandleFunc("GET /metrics", h.PrometheusSelf)

	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(h.Summary)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(h.Ingest)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/metrics/targets", auth(http.HandlerFunc(h.ListTargets)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/metrics/targets", auth(http.HandlerFunc(h.CreateTarget)))
}

func (h *Handler) requireMember(w http.ResponseWriter, r *http.Request, orgID, userID string) bool {
	h.reqCount.Add(1)
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

func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	if h.Simulate {
		_ = h.Store.SeedSimulate(r.Context(), orgID, projectID)
	}
	sum, err := h.Store.Summary(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load metrics")
		return
	}
	targets, _ := h.Store.ListTargets(r.Context(), orgID, projectID)
	promURL := os.Getenv("PROMETHEUS_URL")
	httpx.JSON(w, http.StatusOK, map[string]any{
		"metrics":         sum,
		"targets":         targets,
		"mode":            modeLabel(h.Simulate),
		"prometheus_url":  promURL,
		"scrape_endpoint": "/metrics",
	})
}

func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Name   string            `json:"name"`
		Value  float64           `json:"value"`
		Labels map[string]string `json:"labels"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "name required")
		return
	}
	s := &store.Sample{
		OrgID: orgID, ProjectID: projectID, Name: req.Name, Value: req.Value, Labels: req.Labels,
	}
	if err := h.Store.Ingest(r.Context(), s); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"sample": s})
}

func (h *Handler) ListTargets(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.ListTargets(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	// Prometheus-style scrape annotations for file_sd / HTTP SD consumers
	annotations := make([]map[string]any, 0, len(list))
	for _, t := range list {
		annotations = append(annotations, map[string]any{
			"targets": []string{fmt.Sprintf("host.docker.internal:%d", t.Port)},
			"labels": map[string]string{
				"job":        t.Job,
				"project_id": projectID,
				"org_id":     orgID,
				"__metrics_path__": t.Path,
			},
			"annotations": t.Annotations,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"targets": list, "prometheus_sd": annotations})
}

func (h *Handler) CreateTarget(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req store.Target
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid body")
		return
	}
	req.OrgID = orgID
	req.ProjectID = projectID
	if err := h.Store.UpsertTarget(r.Context(), &req); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"target": req})
}

func (h *Handler) PrometheusSelf(w http.ResponseWriter, r *http.Request) {
	h.reqCount.Add(1)
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP jp_metrics_http_requests_total Requests handled by metrics service\n")
	fmt.Fprintf(w, "# TYPE jp_metrics_http_requests_total counter\n")
	fmt.Fprintf(w, "jp_metrics_http_requests_total %d\n", h.reqCount.Load())
	fmt.Fprintf(w, "# HELP jp_metrics_up Metrics service up\n")
	fmt.Fprintf(w, "# TYPE jp_metrics_up gauge\n")
	fmt.Fprintf(w, "jp_metrics_up 1\n")
	fmt.Fprintf(w, "# HELP jp_metrics_info Build info\n")
	fmt.Fprintf(w, "# TYPE jp_metrics_info gauge\n")
	fmt.Fprintf(w, "jp_metrics_info{service=\"metrics\",mode=\"%s\"} 1\n", modeLabel(h.Simulate))
}

func modeLabel(sim bool) string {
	if sim {
		return "simulate"
	}
	return "live"
}
