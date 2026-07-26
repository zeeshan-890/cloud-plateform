package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jp-cloud/billing/internal/store"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
)

type Plan struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	PriceUSD    float64 `json:"price_usd"`
	BuildMins   int     `json:"build_minutes"`
	RuntimeHrs  int     `json:"runtime_hours"`
	Description string  `json:"description"`
}

var Plans = []Plan{
	{ID: "free", Name: "Free", PriceUSD: 0, BuildMins: 100, RuntimeHrs: 50, Description: "Hobby / evaluate"},
	{ID: "pro", Name: "Pro", PriceUSD: 29, BuildMins: 2000, RuntimeHrs: 500, Description: "Small teams"},
	{ID: "scale", Name: "Scale", PriceUSD: 99, BuildMins: 10000, RuntimeHrs: 2000, Description: "Growing workloads"},
}

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	HTTP            *http.Client
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /billing/plans", auth(http.HandlerFunc(h.ListPlans)))
	mux.Handle("GET /orgs/{orgId}/billing/plans", auth(http.HandlerFunc(h.ListPlans)))
	mux.Handle("GET /orgs/{orgId}/billing/usage", auth(http.HandlerFunc(h.UsageSummary)))
	mux.Handle("GET /orgs/{orgId}/billing/events", auth(http.HandlerFunc(h.ListEvents)))
	mux.Handle("POST /orgs/{orgId}/billing/events", auth(http.HandlerFunc(h.IngestEvent)))
	mux.Handle("GET /orgs/{orgId}/billing/plan", auth(http.HandlerFunc(h.GetPlan)))
	mux.Handle("PUT /orgs/{orgId}/billing/plan", auth(http.HandlerFunc(h.SetPlan)))

	mux.HandleFunc("POST /internal/billing/events", h.InternalIngest)
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

func (h *Handler) ListPlans(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"plans": Plans})
}

func (h *Handler) UsageSummary(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	if d := r.URL.Query().Get("days"); d != "" {
		var days int
		fmt.Sscanf(d, "%d", &days)
		if days > 0 && days <= 365 {
			since = time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
		}
	}
	sum, err := h.Store.Summary(r.Context(), orgID, since)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "summary failed")
		return
	}
	planID, _ := h.Store.GetPlan(r.Context(), orgID)
	var plan *Plan
	for i := range Plans {
		if Plans[i].ID == planID {
			plan = &Plans[i]
			break
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{
		"usage":      sum,
		"since":      since,
		"plan_id":    planID,
		"plan":       plan,
		"stub_note":  "build_minutes and runtime_hours are stub meters from deploy/build events",
	})
}

func (h *Handler) ListEvents(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	list, err := h.Store.ListEvents(r.Context(), orgID, 50)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"events": list})
}

func (h *Handler) IngestEvent(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		ProjectID string  `json:"project_id"`
		Metric    string  `json:"metric"`
		Quantity  float64 `json:"quantity"`
		Unit      string  `json:"unit"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.Metric == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "metric required")
		return
	}
	e := &store.UsageEvent{OrgID: orgID, Metric: req.Metric, Quantity: req.Quantity, Unit: req.Unit, Source: "api"}
	if req.ProjectID != "" {
		e.ProjectID = &req.ProjectID
	}
	if e.Unit == "" {
		e.Unit = defaultUnit(req.Metric)
	}
	if err := h.Store.Insert(r.Context(), e); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"event": e})
}

func (h *Handler) InternalIngest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string  `json:"org_id"`
		ProjectID string  `json:"project_id"`
		Metric    string  `json:"metric"`
		Quantity  float64 `json:"quantity"`
		Unit      string  `json:"unit"`
		Source    string  `json:"source"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.Metric == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and metric required")
		return
	}
	e := &store.UsageEvent{
		OrgID: req.OrgID, Metric: req.Metric, Quantity: req.Quantity,
		Unit: req.Unit, Source: req.Source,
	}
	if e.Source == "" {
		e.Source = "stream"
	}
	if e.Unit == "" {
		e.Unit = defaultUnit(req.Metric)
	}
	if req.ProjectID != "" {
		e.ProjectID = &req.ProjectID
	}
	if err := h.Store.Insert(r.Context(), e); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "ingest failed")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"event": e})
}

func (h *Handler) GetPlan(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	planID, _ := h.Store.GetPlan(r.Context(), orgID)
	httpx.JSON(w, http.StatusOK, map[string]any{"plan_id": planID})
}

func (h *Handler) SetPlan(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		PlanID string `json:"plan_id"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.PlanID == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "plan_id required")
		return
	}
	ok := false
	for _, p := range Plans {
		if p.ID == req.PlanID {
			ok = true
			break
		}
	}
	if !ok {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "unknown plan_id")
		return
	}
	if err := h.Store.SetPlan(r.Context(), orgID, req.PlanID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "update failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"plan_id": req.PlanID})
}

func defaultUnit(metric string) string {
	switch metric {
	case "build_minutes":
		return "minutes"
	case "runtime_hours":
		return "hours"
	default:
		return "units"
	}
}
