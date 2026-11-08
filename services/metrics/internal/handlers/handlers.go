package handlers

import (
	"fmt"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/metrics/internal/store"
)

type Handler struct {
	Store   store.Repo
	JWT     *jwtutil.Manager
	Members *membership.Client
	// Simulate seeds fake series on read. Dev/test only; main refuses it in production.
	Simulate bool
	reqCount atomic.Int64
}

var nameRe = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:.\-]{0,99}$`)

const (
	maxBatch  = 500
	maxLabels = 20
)

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	// Service-level counters only: no tenant labels.
	mux.HandleFunc("GET /metrics", h.PrometheusSelf)

	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(h.Summary)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(h.Ingest)))

	in := func(f http.HandlerFunc) http.Handler { return internalauth.Require(f) }
	mux.Handle("POST /internal/metrics", in(h.InternalIngest))
	mux.Handle("DELETE /internal/projects/{projectId}", in(h.InternalDeleteProject))
}

func validSample(name string, v float64, labels map[string]string) string {
	if !nameRe.MatchString(name) {
		return "invalid metric name"
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "value must be finite"
	}
	if len(labels) > maxLabels {
		return "too many labels"
	}
	for k, val := range labels {
		if len(k) > 64 || len(val) > 256 {
			return "label too long"
		}
	}
	return ""
}

// Summary (viewer+): latest value per metric, or one series with ?name=&since=&limit=.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	h.reqCount.Add(1)
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaMetrics, membership.LevelView); !ok {
		return
	}
	if h.Simulate {
		_ = h.Store.IngestBatch(r.Context(), simulated(orgID, projectID))
	}
	mode := modeLabel(h.Simulate)
	if name := r.URL.Query().Get("name"); name != "" {
		if !nameRe.MatchString(name) {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid metric name")
			return
		}
		since := time.Now().Add(-time.Hour)
		if v := r.URL.Query().Get("since"); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				httpx.Error(w, http.StatusBadRequest, "validation_error", "since must be RFC3339")
				return
			}
			since = t
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		pts, err := h.Store.Series(r.Context(), orgID, projectID, name, since, limit)
		if err != nil {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load series")
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"name": name, "points": pts, "mode": mode})
		return
	}
	sum, err := h.Store.Summary(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to load metrics")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"metrics": sum, "mode": mode})
}

// Ingest (logs/metrics manage) records one sample.
func (h *Handler) Ingest(w http.ResponseWriter, r *http.Request) {
	h.reqCount.Add(1)
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if _, ok := h.Members.RequireArea(w, r, orgID, projectID, uid, membership.AreaMetrics, membership.LevelManage); !ok {
		return
	}
	var req struct {
		Name   string            `json:"name"`
		Value  float64           `json:"value"`
		Labels map[string]string `json:"labels"`
	}
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid body")
		return
	}
	if msg := validSample(req.Name, req.Value, req.Labels); msg != "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
		return
	}
	s := &store.Sample{OrgID: orgID, ProjectID: projectID, Name: req.Name, Value: req.Value, Labels: req.Labels}
	if err := h.Store.IngestBatch(r.Context(), []*store.Sample{s}); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"sample": s})
}

// InternalIngest lets platform services (runtime) push batches of samples.
func (h *Handler) InternalIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Samples []struct {
			OrgID      string            `json:"org_id"`
			ProjectID  string            `json:"project_id"`
			Name       string            `json:"name"`
			Value      float64           `json:"value"`
			Labels     map[string]string `json:"labels"`
			RecordedAt time.Time         `json:"recorded_at"`
		} `json:"samples"`
	}
	if err := httpx.DecodeLimit(r, &req, 4<<20); err != nil || len(req.Samples) == 0 || len(req.Samples) > maxBatch {
		httpx.Error(w, http.StatusBadRequest, "validation_error", fmt.Sprintf("1-%d samples required", maxBatch))
		return
	}
	batch := make([]*store.Sample, 0, len(req.Samples))
	for _, s := range req.Samples {
		if s.OrgID == "" || s.ProjectID == "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "org_id and project_id required on every sample")
			return
		}
		if msg := validSample(s.Name, s.Value, s.Labels); msg != "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", msg)
			return
		}
		batch = append(batch, &store.Sample{OrgID: s.OrgID, ProjectID: s.ProjectID, Name: s.Name, Value: s.Value, Labels: s.Labels, RecordedAt: s.RecordedAt})
	}
	if err := h.Store.IngestBatch(r.Context(), batch); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"ingested": len(batch)})
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	if err := h.Store.DeleteProject(r.Context(), r.PathValue("projectId")); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// PrometheusSelf exposes service-level counters only (no per-tenant labels).
func (h *Handler) PrometheusSelf(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "# HELP jp_metrics_http_requests_total Authenticated metric API requests handled\n# TYPE jp_metrics_http_requests_total counter\njp_metrics_http_requests_total %d\n", h.reqCount.Load())
	fmt.Fprintf(w, "# HELP jp_metrics_up Metrics service up\n# TYPE jp_metrics_up gauge\njp_metrics_up 1\n")
}

func modeLabel(sim bool) string {
	if sim {
		return "simulate"
	}
	return "live"
}

// SimulateAllowed reports whether METRICS_MODE=simulate may be honoured (never in production).
func SimulateAllowed(env string) bool { return env != "production" }

func simulated(orgID, projectID string) []*store.Sample {
	now := time.Now().UTC()
	vals := map[string]float64{
		"http_requests_total": float64(100 + now.Second()*3), "http_request_duration_ms": 42 + float64(now.Second()%10), "runtime_instances_up": 1,
	}
	out := []*store.Sample{}
	for n, v := range vals {
		out = append(out, &store.Sample{OrgID: orgID, ProjectID: projectID, Name: n, Value: v, Labels: map[string]string{"mode": "simulate"}, RecordedAt: now})
	}
	return out
}

// RetentionDays reads METRICS_RETENTION_DAYS (default 14).
func RetentionDays() int {
	if n, err := strconv.Atoi(os.Getenv("METRICS_RETENTION_DAYS")); err == nil && n > 0 {
		return n
	}
	return 14
}
