package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/domain/internal/dnscheck"
	"github.com/jp-cloud/domain/internal/store"
	"github.com/jp-cloud/domain/internal/traefikcfg"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	Store            *store.Store
	JWT              *jwtutil.Manager
	OrganizationURL  string
	CertificateURL   string
	Traefik          *traefikcfg.Writer
	Redis            *redis.Client
	HTTP             *http.Client
	DNSStub          bool
	CNAMETarget      string
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(h.List)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/domains/{domainId}/verify", auth(http.HandlerFunc(h.Verify)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(h.Delete)))

	mux.HandleFunc("POST /internal/domains/preview", h.InternalPreviewProvision)
	mux.HandleFunc("DELETE /internal/domains/by-deployment/{deploymentId}", h.InternalDeleteByDeployment)
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
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list domains")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"domains": list})
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Hostname         string `json:"hostname"`
		DeploymentID     string `json:"deployment_id"`
		VerificationType string `json:"verification_type"`
	}
	if err := httpx.Decode(r, &req); err != nil || strings.TrimSpace(req.Hostname) == "" {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "hostname required")
		return
	}
	host := strings.ToLower(strings.TrimSpace(req.Hostname))
	if req.VerificationType == "" {
		req.VerificationType = "cname"
	}
	d := &store.Domain{
		OrgID: orgID, ProjectID: projectID, Hostname: host,
		Status: "pending", VerificationType: req.VerificationType,
		VerificationToken: "jp-" + uuid.NewString()[:8],
	}
	if req.DeploymentID != "" {
		d.DeploymentID = &req.DeploymentID
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "hostname already registered")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create domain")
		return
	}
	h.publish(r.Context(), events.TypeDomainAdded, uid, orgID, map[string]any{
		"domain_id": d.ID, "hostname": d.Hostname, "project_id": projectID,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{
		"domain": d,
		"instructions": map[string]string{
			"cname": "Point CNAME to " + h.cnameTarget(),
			"txt":   "Add TXT record jp-verify=" + d.VerificationToken + " on " + host,
		},
	})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("domainId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"domain": d})
}

func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("domainId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	var req struct {
		Force bool `json:"force"`
	}
	_ = httpx.Decode(r, &req)

	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}

	stub := h.DNSStub || req.Force
	result := dnscheck.WithTimeout(d.Hostname, d.VerificationType, d.VerificationToken, h.cnameTarget(), stub)
	if !result.OK {
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"verified": false,
			"dns":      result,
			"domain":   d,
		})
		return
	}

	now := time.Now().UTC()
	d.Status = "verified"
	d.VerifiedAt = &now
	d.ForceVerified = req.Force
	if path, err := h.Traefik.WriteDomain(d.Hostname, projectID, true); err == nil {
		d.TraefikFile = path
	}

	certID := h.requestCert(r.Context(), orgID, projectID, d)
	if certID != "" {
		d.CertificateID = &certID
		d.Status = "active"
	}

	_ = h.Store.Update(r.Context(), d)
	h.publish(r.Context(), events.TypeDomainVerified, uid, orgID, map[string]any{
		"domain_id": d.ID, "hostname": d.Hostname, "project_id": projectID, "force": req.Force,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"verified": true, "dns": result, "domain": d})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID := r.PathValue("orgId")
	projectID := r.PathValue("projectId")
	id := r.PathValue("domainId")
	if !h.requireMember(w, r, orgID, uid) {
		return
	}
	d, err := h.Store.Get(r.Context(), orgID, projectID, id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed")
		return
	}
	_ = h.Traefik.RemoveDomain(d.Hostname)
	if err := h.Store.Delete(r.Context(), orgID, projectID, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) requestCert(ctx context.Context, orgID, projectID string, d *store.Domain) string {
	if h.CertificateURL == "" || h.HTTP == nil {
		return ""
	}
	body, _ := json.Marshal(map[string]any{
		"org_id": orgID, "project_id": projectID, "domain_id": d.ID, "hostname": d.Hostname,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.CertificateURL, "/")+"/internal/certificates", bytes.NewReader(body))
	if err != nil {
		return ""
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return ""
	}
	var out struct {
		Certificate struct {
			ID string `json:"id"`
		} `json:"certificate"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Certificate.ID
}

func (h *Handler) publish(ctx context.Context, typ, actor, org string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDomain, typ, actor, org, payload)
	_, _ = events.PublishJSON(ctx, h.Redis, events.TopicDomain, env)
}

func (h *Handler) cnameTarget() string {
	if h.CNAMETarget != "" {
		return h.CNAMETarget
	}
	if v := os.Getenv("DOMAIN_CNAME_TARGET"); v != "" {
		return v
	}
	return "cname.jp.localhost"
}

// InternalPreviewProvision force-verifies a preview hostname and writes Traefik config.
func (h *Handler) InternalPreviewProvision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID        string `json:"org_id"`
		ProjectID    string `json:"project_id"`
		DeploymentID string `json:"deployment_id"`
		Hostname     string `json:"hostname"`
	}
	if err := httpx.Decode(r, &req); err != nil || req.OrgID == "" || req.ProjectID == "" || req.Hostname == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id, hostname required")
		return
	}
	host := strings.ToLower(strings.TrimSpace(req.Hostname))

	// Replace any existing domains for this deployment
	if req.DeploymentID != "" {
		if existing, err := h.Store.FindByDeploymentID(r.Context(), req.DeploymentID); err == nil {
			for _, d := range existing {
				_ = h.Traefik.RemoveDomain(d.Hostname)
				_ = h.Store.DeleteByID(r.Context(), d.ID)
			}
		}
	}

	now := time.Now().UTC()
	d := &store.Domain{
		OrgID: req.OrgID, ProjectID: req.ProjectID, Hostname: host,
		Status: "active", VerificationType: "cname",
		VerificationToken: "jp-preview", ForceVerified: true, VerifiedAt: &now,
	}
	if req.DeploymentID != "" {
		d.DeploymentID = &req.DeploymentID
	}

	if existing, err := h.Store.FindByHostname(r.Context(), host); err == nil {
		_ = h.Traefik.RemoveDomain(existing.Hostname)
		_ = h.Store.DeleteByID(r.Context(), existing.ID)
	}

	if err := h.Store.Create(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "hostname already registered")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create domain")
		return
	}
	if path, err := h.Traefik.WriteDomain(d.Hostname, req.ProjectID, false); err == nil {
		d.TraefikFile = path
		_ = h.Store.Update(r.Context(), d)
	}

	url := "http://" + host
	h.publish(r.Context(), events.TypeDomainVerified, "", req.OrgID, map[string]any{
		"domain_id": d.ID, "hostname": d.Hostname, "project_id": req.ProjectID, "preview": true,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"domain": d, "url": url, "preview": true})
}

func (h *Handler) InternalDeleteByDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID := r.PathValue("deploymentId")
	if deploymentID == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "deploymentId required")
		return
	}
	list, err := h.Store.FindByDeploymentID(r.Context(), deploymentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	removed := 0
	for _, d := range list {
		_ = h.Traefik.RemoveDomain(d.Hostname)
		if err := h.Store.DeleteByID(r.Context(), d.ID); err == nil {
			removed++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed, "deployment_id": deploymentID})
}
