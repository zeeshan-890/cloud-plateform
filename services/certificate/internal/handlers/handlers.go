package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jp-cloud/certificate/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store           *store.Store
	JWT             *jwtutil.Manager
	OrganizationURL string
	Redis           *redis.Client
	HTTP            *http.Client
	SimulateACME    bool
	CertResolver    string
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/certificates", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/certificates/{certId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/certificates/{certId}/renew", auth(http.HandlerFunc(h.Renew)))

	mux.HandleFunc("POST /internal/certificates", h.InternalCreate)
	mux.HandleFunc("POST /internal/certificates/renew-scan", h.InternalRenewScan)
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
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list certificates")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"certificates": list})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("certId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	c, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "certificate not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"certificate": c})
}

func (h *Handler) Renew(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("certId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	c, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "certificate not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	h.applyRenew(r.Context(), c, uid)
	httpx.JSON(w, http.StatusOK, map[string]any{"certificate": c})
}

func (h *Handler) InternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		DomainID  string `json:"domain_id"`
		Hostname  string `json:"hostname"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.Hostname == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id and hostname required")
		return
	}
	c := &store.Certificate{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Hostname: req.Hostname,
		Status: "pending", Provider: "traefik-acme", Resolver: h.CertResolver,
		Metadata: mustJSON(map[string]any{
			"traefik_labels": map[string]string{
				"traefik.enable": "true",
				"traefik.http.routers." + sanitize(req.Hostname) + ".tls.certresolver": firstNonEmpty(h.CertResolver, "letsencrypt"),
			},
			"mode": map[string]any{"simulate": h.SimulateACME},
		}),
	}
	if req.DomainID != "" {
		c.DomainID = &req.DomainID
	}
	if err := h.Store.Create(r.Context(), c); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create certificate")
		return
	}
	// Simulate ACME issue (Traefik handles real ACME when resolver configured).
	if h.SimulateACME || h.CertResolver == "" {
		now := time.Now().UTC()
		exp := now.Add(90 * 24 * time.Hour)
		c.Status = "issued"
		c.IssuedAt = &now
		c.ExpiresAt = &exp
		c.Error = ""
		meta := map[string]any{
			"note": "simulated ACME issue; Traefik file provider attaches router when domain verified",
			"resolver": firstNonEmpty(h.CertResolver, "none"),
		}
		c.Metadata = mustJSON(meta)
		_ = h.Store.Update(r.Context(), c)
		h.publish(r.Context(), events.TypeCertIssued, "", req.OrgID, map[string]any{
			"certificate_id": c.ID, "hostname": c.Hostname, "status": c.Status,
		})
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"certificate": c})
}

func (h *Handler) InternalRenewScan(w http.ResponseWriter, r *http.Request) {
	list, err := h.Store.ListExpiring(r.Context())
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "scan failed")
		return
	}
	renewed := 0
	for i := range list {
		h.applyRenew(r.Context(), &list[i], "")
		renewed++
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"scanned": len(list), "renewed": renewed})
}

func (h *Handler) applyRenew(ctx context.Context, c *store.Certificate, actor string) {
	c.Status = "renewing"
	_ = h.Store.Update(ctx, c)
	now := time.Now().UTC()
	exp := now.Add(90 * 24 * time.Hour)
	c.Status = "issued"
	c.RenewedAt = &now
	c.ExpiresAt = &exp
	if c.IssuedAt == nil {
		c.IssuedAt = &now
	}
	c.Metadata = mustJSON(map[string]any{
		"renewed_via": "api",
		"simulate":    h.SimulateACME,
		"resolver":    firstNonEmpty(h.CertResolver, "none"),
	})
	_ = h.Store.Update(ctx, c)
	h.publish(ctx, events.TypeCertRenewed, actor, c.OrgID, map[string]any{
		"certificate_id": c.ID, "hostname": c.Hostname, "expires_at": exp,
	})
}

func (h *Handler) publish(ctx context.Context, typ, actor, org string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDomain, typ, actor, org, payload)
	_, _ = events.PublishJSON(ctx, h.Redis, events.TopicDomain, env)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func sanitize(host string) string {
	h := strings.ToLower(host)
	h = strings.ReplaceAll(h, ".", "-")
	return h
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// SimulateFromEnv reads CERT_SIMULATE (default true).
func SimulateFromEnv() bool {
	v := os.Getenv("CERT_SIMULATE")
	if v == "" {
		return true
	}
	return v == "true" || v == "1"
}
