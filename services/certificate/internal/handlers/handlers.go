package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/certificate/internal/prober"
	"github.com/jp-cloud/certificate/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

// Authz is implemented by *membership.Client.
type Authz interface {
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

// Prober is implemented by *prober.Prober.
type Prober interface {
	Probe(ctx context.Context, host string) prober.Result
}

// Reissuer asks the domain service to make Traefik retry ACME.
type Reissuer interface {
	Reissue(ctx context.Context, domainID string) error
}

type Handler struct {
	Store        store.Store
	JWT          *jwtutil.Manager
	Authz        Authz
	Prober       Prober
	Reissuer     Reissuer
	Redis        *redis.Client
	Log          *slog.Logger
	TLSEnabled   bool
	Resolver     string
	PendingGrace time.Duration // default 30m
	RenewCool    time.Duration // default 60s
	Now          func() time.Time
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now().UTC()
	}
	return time.Now().UTC()
}

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) grace() time.Duration {
	if h.PendingGrace > 0 {
		return h.PendingGrace
	}
	return 30 * time.Minute
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/certificates", auth(http.HandlerFunc(h.List)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/certificates/{certId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/certificates/{certId}/renew", auth(http.HandlerFunc(h.Renew)))

	mux.Handle("POST /internal/certificates", internalauth.Require(http.HandlerFunc(h.InternalCreate)))
	mux.Handle("POST /internal/certificates/scan", internalauth.Require(http.HandlerFunc(h.InternalScan)))
	mux.Handle("DELETE /internal/certificates/by-domain/{domainId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteByDomain)))
	mux.Handle("GET /internal/projects/{projectId}/certificates", internalauth.Require(http.HandlerFunc(h.InternalListByProject)))
	mux.Handle("DELETE /internal/projects/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteProject)))
}

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

type view struct {
	store.Certificate
	DaysLeft   *int `json:"days_left,omitempty"`
	TLSEnabled bool `json:"tls_enabled"`
}

func (h *Handler) view(c store.Certificate) view {
	v := view{Certificate: c, TLSEnabled: h.TLSEnabled}
	if c.ExpiresAt != nil && (c.Status == prober.StatusIssued || c.Status == prober.StatusExpiring) {
		d := int(c.ExpiresAt.Sub(h.now()).Hours() / 24)
		v.DaysLeft = &d
	}
	return v
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelView); !ok {
		return
	}
	list, err := h.Store.List(r.Context(), orgID, projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list certificates")
		return
	}
	out := make([]view, 0, len(list))
	for _, c := range list {
		out = append(out, h.view(c))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"certificates": out, "tls_enabled": h.TLSEnabled})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID, id := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("certId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelView); !ok {
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
	httpx.JSON(w, http.StatusOK, map[string]any{"certificate": h.view(*c)})
}

// Renew re-probes and, when there is no valid certificate (or it is expiring), asks Traefik to
// retry ACME by having the domain service re-write the router. Valid certificates are left to
// Traefik's own automatic renewal.
func (h *Handler) Renew(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID, id := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("certId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelManage); !ok {
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
	if !h.TLSEnabled {
		h.ProbeOne(r.Context(), c)
		httpx.Error(w, http.StatusConflict, "tls_disabled", "TLS is disabled on this platform; no certificates are issued")
		return
	}
	cool := h.RenewCool
	if cool <= 0 {
		cool = 60 * time.Second
	}
	if c.ReissueRequestedAt != nil && h.now().Sub(*c.ReissueRequestedAt) < cool {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "a re-issue was requested moments ago; wait a minute")
		return
	}
	h.ProbeOne(r.Context(), c)
	if c.Status == prober.StatusIssued {
		httpx.JSON(w, http.StatusOK, map[string]any{"certificate": h.view(*c), "action": "none",
			"message": "certificate is valid; Traefik renews it automatically before expiry"})
		return
	}
	if c.DomainID == nil || h.Reissuer == nil {
		httpx.Error(w, http.StatusConflict, "no_domain", "certificate is not bound to a domain")
		return
	}
	if err := h.Reissuer.Reissue(r.Context(), *c.DomainID); err != nil {
		httpx.Error(w, http.StatusBadGateway, "bad_gateway", "re-issue request failed: "+err.Error())
		return
	}
	now := h.now()
	c.ReissueRequestedAt, c.RenewedAt = &now, &now
	_ = h.Store.Update(r.Context(), c)
	httpx.JSON(w, http.StatusAccepted, map[string]any{"certificate": h.view(*c), "action": "reissue_requested",
		"message": "Traefik was asked to retry issuance; status updates after the next probe"})
}

// ProbeOne refreshes one certificate from reality and persists/publishes changes.
func (h *Handler) ProbeOne(ctx context.Context, c *store.Certificate) {
	prev := c.Status
	prevSerial := c.Serial
	now := h.now()
	c.LastProbedAt = &now
	if !h.TLSEnabled {
		c.Status, c.Error = prober.StatusTLSDisabled, ""
	} else if strings.HasPrefix(c.Hostname, "*.") || h.Prober == nil {
		return
	} else {
		res := h.Prober.Probe(ctx, c.Hostname)
		since := c.CreatedAt
		if c.ReissueRequestedAt != nil && c.ReissueRequestedAt.After(since) {
			since = *c.ReissueRequestedAt
		}
		ev := prober.Evaluate(res, now, since, h.grace())
		c.Status, c.Error = ev.Status, ev.Error
		if res.Reachable && res.ChainOK {
			na, nb := res.NotAfter, res.NotBefore
			c.ExpiresAt, c.IssuedAt = &na, &nb
			c.Issuer, c.Serial = res.Issuer, res.Serial
			c.SANs, _ = json.Marshal(res.DNSNames)
			c.Metadata, _ = json.Marshal(map[string]any{"staging": res.Staging})
		}
	}
	if err := h.Store.Update(ctx, c); err != nil {
		h.log().Error("certificate update", "id", c.ID, "err", err)
		return
	}
	issuedNow := c.Status == prober.StatusIssued || c.Status == prober.StatusExpiring
	wasIssued := prev == prober.StatusIssued || prev == prober.StatusExpiring
	switch {
	case issuedNow && !wasIssued:
		h.publish(ctx, events.TypeCertIssued, c)
	case issuedNow && wasIssued && prevSerial != "" && prevSerial != c.Serial:
		h.publish(ctx, events.TypeCertRenewed, c)
	case c.Status == prober.StatusExpiring && prev != prober.StatusExpiring:
		h.publish(ctx, "cert.expiring", c)
	case c.Status == prober.StatusFailed && prev != prober.StatusFailed:
		h.publish(ctx, "cert.failed", c)
	}
}

// Scan probes every certificate that is due (non-issued every run, issued hourly).
func (h *Handler) Scan(ctx context.Context) (probed int) {
	list, err := h.Store.ListAll(ctx)
	if err != nil {
		h.log().Error("certificate scan", "err", err)
		return 0
	}
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := range list {
		c := list[i]
		valid := c.Status == prober.StatusIssued || c.Status == prober.StatusExpiring
		if valid && c.LastProbedAt != nil && h.now().Sub(*c.LastProbedAt) < time.Hour && h.TLSEnabled {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			h.ProbeOne(ctx, &c)
			mu.Lock()
			probed++
			mu.Unlock()
		}()
	}
	wg.Wait()
	return probed
}

func (h *Handler) InternalScan(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, map[string]any{"probed": h.Scan(r.Context())})
}

func (h *Handler) InternalCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID     string `json:"org_id"`
		ProjectID string `json:"project_id"`
		DomainID  string `json:"domain_id"`
		Hostname  string `json:"hostname"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validID(req.OrgID) || !validID(req.ProjectID) || !validID(req.DomainID) || req.Hostname == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id, domain_id (uuids) and hostname required")
		return
	}
	c := &store.Certificate{OrgID: req.OrgID, ProjectID: req.ProjectID, DomainID: &req.DomainID, Hostname: strings.ToLower(req.Hostname),
		Status: prober.StatusPending, Provider: "traefik-acme", Resolver: h.Resolver}
	if !h.TLSEnabled {
		c.Status = prober.StatusTLSDisabled
	}
	out, err := h.Store.Upsert(r.Context(), c)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to register certificate")
		return
	}
	httpx.JSON(w, http.StatusCreated, map[string]any{"certificate": h.view(*out)})
}

func (h *Handler) InternalDeleteByDomain(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("domainId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	n, err := h.Store.DeleteByDomain(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": n})
}

func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	n, err := h.Store.DeleteByProject(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": n})
}

func (h *Handler) InternalListByProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("projectId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	list, err := h.Store.ListByProject(r.Context(), id)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "list failed")
		return
	}
	out := make([]view, 0, len(list))
	for _, c := range list {
		out = append(out, h.view(c))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"certificates": out})
}

func (h *Handler) publish(ctx context.Context, typ string, c *store.Certificate) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDomain, typ, "", c.OrgID, map[string]any{
		"certificate_id": c.ID, "hostname": c.Hostname, "status": c.Status, "expires_at": c.ExpiresAt,
	})
	if _, err := events.PublishJSON(ctx, h.Redis, events.TopicDomain, env); err != nil {
		h.log().Warn("publish event", "type", typ, "err", err)
	}
}

// DomainReissuer calls the domain service.
type DomainReissuer struct {
	BaseURL string
	HTTP    *http.Client
}

func (d *DomainReissuer) Reissue(ctx context.Context, domainID string) error {
	if d == nil || d.BaseURL == "" {
		return errors.New("domain service not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(d.BaseURL, "/")+"/internal/domains/"+domainID+"/reissue", bytes.NewReader(nil))
	if err != nil {
		return err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("domain service status %d", resp.StatusCode)
	}
	return nil
}
