package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jp-cloud/domain/internal/certclient"
	"github.com/jp-cloud/domain/internal/dnscheck"
	"github.com/jp-cloud/domain/internal/hostname"
	"github.com/jp-cloud/domain/internal/store"
	"github.com/jp-cloud/domain/internal/traefikcfg"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/redis/go-redis/v9"
)

// Authz is implemented by *membership.Client (and by fakes in tests).
type Authz interface {
	Require(w http.ResponseWriter, r *http.Request, orgID, userID string, min membership.Role) (membership.Role, bool)
	RequireArea(w http.ResponseWriter, r *http.Request, orgID, projectID, userID string, area membership.Area, need membership.Level) (*membership.Access, bool)
}

// DNSVerifier is implemented by *dnscheck.Verifier.
type DNSVerifier interface {
	Verify(ctx context.Context, hostname, token, preferred string) dnscheck.Result
}

type Handler struct {
	Store    store.Store
	JWT      *jwtutil.Manager
	Authz    Authz
	Files    *traefikcfg.Manager
	Policy   hostname.Policy
	Verifier DNSVerifier
	Certs    certclient.Client
	Redis    *redis.Client
	Log      *slog.Logger

	TLSEnabled  bool
	PreviewTLS  bool
	CNAMETarget string
	ATargets    []string

	// ReissueGap is how long a router stays removed so Traefik sees a config change and retries ACME (default 3s).
	ReissueGap time.Duration
	reissueSem chan struct{}

	// VerifyCooldown is the minimum time between manual verifications of one domain (default 3s).
	VerifyCooldown time.Duration

	mu         sync.Mutex
	lastVerify map[string]time.Time
}

var (
	serviceRe = regexp.MustCompile(`^jp-(app|prev)-[a-z0-9][a-z0-9-]{0,58}$`)
)

func (h *Handler) log() *slog.Logger {
	if h.Log != nil {
		return h.Log
	}
	return slog.Default()
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	auth := middleware.BearerAuth(h.JWT)

	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(h.List)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(h.Get)))
	mux.Handle("POST /orgs/{orgId}/projects/{projectId}/domains/{domainId}/verify", auth(http.HandlerFunc(h.Verify)))
	mux.Handle("DELETE /orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(h.Delete)))

	mux.Handle("POST /internal/domains/preview", internalauth.Require(http.HandlerFunc(h.InternalPreviewProvision)))
	mux.Handle("DELETE /internal/domains/by-deployment/{deploymentId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteByDeployment)))
	mux.Handle("PUT /internal/routes/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalSetRoute)))
	mux.Handle("POST /internal/domains/system", internalauth.Require(http.HandlerFunc(h.InternalSystemCreate)))
	mux.Handle("DELETE /internal/domains/system/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalSystemDelete)))
	mux.Handle("POST /internal/domains/{domainId}/reissue", internalauth.Require(http.HandlerFunc(h.InternalReissue)))
	mux.Handle("DELETE /internal/projects/{projectId}", internalauth.Require(http.HandlerFunc(h.InternalDeleteProject)))
}

// ---------------------------------------------------------------- views

// Record is one DNS record the customer should publish.
type Record struct {
	Purpose string `json:"purpose"` // ownership | routing
	Method  string `json:"method"`  // txt | cname | a
	Type    string `json:"type"`    // TXT | CNAME | A | AAAA
	Name    string `json:"name"`
	Value   string `json:"value"`
}

// Verification describes how to prove ownership.
type Verification struct {
	Token    string   `json:"token"`
	Accepted []string `json:"accepted_methods"`
	Records  []Record `json:"records"`
}

// View is the API representation of a domain.
type View struct {
	store.Domain
	Routed       bool          `json:"routed"`
	SSLStatus    string        `json:"ssl_status"`
	SSLExpiresAt *time.Time    `json:"ssl_expires_at,omitempty"`
	SSLError     string        `json:"ssl_error,omitempty"`
	URL          string        `json:"url,omitempty"`
	Verification *Verification `json:"verification,omitempty"`
}

func (h *Handler) verification(d *store.Domain) *Verification {
	if d.Kind != store.KindCustom {
		return nil
	}
	v := &Verification{Token: d.VerificationToken, Accepted: []string{dnscheck.MethodTXT}}
	v.Records = append(v.Records, Record{Purpose: "ownership", Method: "txt", Type: "TXT",
		Name: dnscheck.ChallengePrefix + d.Hostname, Value: dnscheck.TXTValue(d.VerificationToken)})
	if h.CNAMETarget != "" {
		v.Accepted = append(v.Accepted, dnscheck.MethodCNAME)
		v.Records = append(v.Records, Record{Purpose: "routing", Method: "cname", Type: "CNAME", Name: d.Hostname, Value: h.CNAMETarget})
	}
	for _, ip := range h.ATargets {
		t := "A"
		if strings.Contains(ip, ":") {
			t = "AAAA"
		}
		v.Records = append(v.Records, Record{Purpose: "routing", Method: "a", Type: t, Name: d.Hostname, Value: ip})
	}
	if len(h.ATargets) > 0 {
		v.Accepted = append(v.Accepted, dnscheck.MethodA)
	}
	return v
}

func (h *Handler) scheme() string {
	if h.TLSEnabled {
		return "https://"
	}
	return "http://"
}

func (h *Handler) view(d store.Domain, certs map[string]certclient.Info, certsErr bool) View {
	v := View{Domain: d, Routed: store.Routed(d.Status), Verification: h.verification(&d)}
	switch {
	case !store.Routed(d.Status):
		v.SSLStatus = "none"
	case !h.TLSEnabled:
		v.SSLStatus = "tls_disabled"
	case certsErr:
		v.SSLStatus = "unknown"
	default:
		if ci, ok := certs[d.ID]; ok {
			v.SSLStatus = ci.Status
			v.SSLExpiresAt = ci.ExpiresAt
			v.SSLError = ci.Error
		} else {
			v.SSLStatus = "pending"
		}
	}
	if v.Routed && !d.Wildcard {
		v.URL = h.scheme() + d.Hostname
	}
	return v
}

// ---------------------------------------------------------------- public API

func validID(s string) bool { _, err := uuid.Parse(s); return err == nil }

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
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to list domains")
		return
	}
	var certs map[string]certclient.Info
	certsErr := false
	if h.TLSEnabled && h.Certs != nil {
		c, err := h.Certs.ListByProject(r.Context(), projectID)
		if err != nil {
			certsErr = true
		}
		certs = c
	}
	views := make([]View, 0, len(list))
	for _, d := range list {
		views = append(views, h.view(d, certs, certsErr))
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"domains": views, "config": h.publicConfig()})
}

func (h *Handler) publicConfig() map[string]any {
	return map[string]any{
		"tls_enabled": h.TLSEnabled, "cname_target": h.CNAMETarget, "a_targets": h.ATargets,
		"root_domain": h.Policy.RootDomain,
	}
}

type createReq struct {
	Hostname         string `json:"hostname"`
	VerificationType string `json:"verification_type"`
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID := r.PathValue("orgId"), r.PathValue("projectId")
	if !validID(orgID) || !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelManage); !ok {
		return
	}
	var req createReq
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", "invalid request body")
		return
	}
	host := hostname.Normalize(req.Hostname)
	if err := h.Policy.ValidateCustom(host); err != nil {
		code := "validation_error"
		if errors.Is(err, hostname.ErrReserved) {
			code = "reserved_hostname"
		}
		httpx.Error(w, http.StatusBadRequest, code, err.Error())
		return
	}
	vt := strings.ToLower(strings.TrimSpace(req.VerificationType))
	switch vt {
	case "":
		vt = dnscheck.MethodTXT
	case dnscheck.MethodTXT:
	case dnscheck.MethodCNAME:
		if h.CNAMETarget == "" {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "CNAME verification is not configured on this platform")
			return
		}
	case dnscheck.MethodA:
		if len(h.ATargets) == 0 {
			httpx.Error(w, http.StatusBadRequest, "validation_error", "A-record verification is not configured on this platform")
			return
		}
	default:
		httpx.Error(w, http.StatusBadRequest, "validation_error", "verification_type must be txt, cname or a")
		return
	}
	token, err := dnscheck.NewToken()
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "token generation failed")
		return
	}
	d := &store.Domain{
		OrgID: orgID, ProjectID: projectID, Hostname: host, Kind: store.KindCustom,
		Status: store.StatusPending, VerificationType: vt, VerificationToken: token,
	}
	if err := h.Store.Create(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "hostname already registered in this project")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create domain")
		return
	}
	h.publish(r.Context(), events.TypeDomainAdded, uid, orgID, map[string]any{
		"domain_id": d.ID, "hostname": d.Hostname, "project_id": projectID,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"domain": h.view(*d, nil, false), "verification": h.verification(d)})
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID, id := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("domainId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelView); !ok {
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
	var certs map[string]certclient.Info
	certsErr := false
	if h.TLSEnabled && h.Certs != nil {
		c, err := h.Certs.ListByProject(r.Context(), projectID)
		certsErr = err != nil
		certs = c
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"domain": h.view(*d, certs, certsErr)})
}

func (h *Handler) cooldown() time.Duration {
	if h.VerifyCooldown > 0 {
		return h.VerifyCooldown
	}
	return 3 * time.Second
}

func (h *Handler) throttled(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastVerify == nil {
		h.lastVerify = map[string]time.Time{}
	}
	if len(h.lastVerify) > 2048 {
		for k, t := range h.lastVerify {
			if time.Since(t) > time.Minute {
				delete(h.lastVerify, k)
			}
		}
	}
	if t, ok := h.lastVerify[key]; ok && time.Since(t) < h.cooldown() {
		return true
	}
	h.lastVerify[key] = time.Now()
	return false
}

// Verify performs a real DNS check. There is intentionally no "force" option.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID, id := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("domainId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelManage); !ok {
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
	if d.Kind != store.KindCustom {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "platform-managed domains are verified automatically")
		return
	}
	if h.throttled(d.ID) {
		httpx.Error(w, http.StatusTooManyRequests, "rate_limited", "verification was just attempted; wait a few seconds")
		return
	}

	res := h.Verifier.Verify(r.Context(), d.Hostname, d.VerificationToken, d.VerificationType)
	now := time.Now().UTC()
	d.DNSLastChecked = &now
	if !res.OK {
		d.DNSError = truncate(res.Detail, 500)
		_ = h.Store.Update(r.Context(), d)
		httpx.JSON(w, http.StatusBadRequest, map[string]any{
			"verified": false, "dns": res, "domain": h.view(*d, nil, false), "verification": h.verification(d),
			"error": map[string]string{"code": "verification_failed", "message": res.Detail},
		})
		return
	}

	wasRouted := store.Routed(d.Status)
	d.Status = store.StatusVerified
	d.VerifiedAt = &now
	d.VerifiedMethod = res.Method
	d.DNSError = ""
	d.DNSFailCount = 0
	if err := h.Store.Update(r.Context(), d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "this hostname is already verified by another project")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to save verification")
		return
	}
	if err := h.SyncProject(r.Context(), projectID); err != nil {
		h.log().Error("sync routers", "project", projectID, "err", err)
	}
	if !wasRouted || d.CertificateID == nil {
		h.registerCert(r.Context(), d)
	}
	if !wasRouted {
		h.publish(r.Context(), events.TypeDomainVerified, uid, orgID, map[string]any{
			"domain_id": d.ID, "hostname": d.Hostname, "project_id": projectID, "method": res.Method,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"verified": true, "dns": res, "domain": h.view(*d, nil, false)})
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	uid := middleware.UserIDFrom(r.Context())
	orgID, projectID, id := r.PathValue("orgId"), r.PathValue("projectId"), r.PathValue("domainId")
	if !validID(orgID) || !validID(projectID) || !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid id")
		return
	}
	if _, ok := h.Authz.RequireArea(w, r, orgID, projectID, uid, membership.AreaDomains, membership.LevelManage); !ok {
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
	if d.Kind != store.KindCustom {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "platform-managed domains are removed with the project")
		return
	}
	if err := h.Files.Remove(d.Hostname, d.Wildcard); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to remove router")
		return
	}
	if err := h.Store.Delete(r.Context(), orgID, projectID, id); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	if h.Certs != nil {
		if err := h.Certs.DeleteByDomain(r.Context(), d.ID); err != nil {
			h.log().Warn("certificate cleanup", "domain", d.ID, "err", err)
		}
	}
	h.publish(r.Context(), "domain.deleted", uid, orgID, map[string]any{"domain_id": d.ID, "hostname": d.Hostname, "project_id": projectID})
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------- routing

// ShortID returns the first 8 hex chars of a UUID without dashes.
func ShortID(id string) string {
	s := strings.ReplaceAll(strings.ToLower(id), "-", "")
	if len(s) > 8 {
		s = s[:8]
	}
	return s
}

// DefaultService is the production traefik service of a project (CONTRACT: jp-app-<short>).
func DefaultService(projectID string) string { return "jp-app-" + ShortID(projectID) }

// PreviewService is the traefik service of a preview deployment (CONTRACT: jp-prev-<deploymentShort8>).
func PreviewService(deploymentID string) string { return "jp-prev-" + ShortID(deploymentID) }

func (h *Handler) routeFor(d store.Domain, routes map[string]string, ups map[string][]string) (traefikcfg.Route, bool) {
	svc := ""
	var upstreams []string
	switch {
	case d.Service != nil && *d.Service != "":
		svc = *d.Service
	case routes[d.ProjectID] != "":
		svc = routes[d.ProjectID]
		upstreams = ups[d.ProjectID]
	default:
		svc = DefaultService(d.ProjectID)
		upstreams = ups[d.ProjectID]
	}
	tls := h.TLSEnabled
	if d.Kind == store.KindPreview {
		tls = h.PreviewTLS && !d.Wildcard
	}
	if hostname.ValidSyntax(d.Hostname) != nil {
		return traefikcfg.Route{}, false
	}
	return traefikcfg.Route{Hostname: d.Hostname, Wildcard: d.Wildcard, Service: svc, TLS: tls, Upstreams: upstreams}, true
}

// SyncProject (re)writes the router files of one project's domains from the DB: routed
// domains get a file pointing at the project's current service, everything else loses its file.
func (h *Handler) SyncProject(ctx context.Context, projectID string) error {
	list, err := h.Store.ListByProject(ctx, projectID)
	if err != nil {
		return err
	}
	svc, err := h.Store.GetRoute(ctx, projectID)
	routes := map[string]string{}
	ups := map[string][]string{}
	if err == nil {
		routes[projectID] = svc
		all, uerr := h.Store.ListUpstreams(ctx)
		if uerr != nil {
			return uerr
		}
		if u := all[projectID]; len(u) > 0 {
			ups[projectID] = u
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var firstErr error
	for i := range list {
		d := list[i]
		if !store.Routed(d.Status) {
			if e := h.Files.Remove(d.Hostname, d.Wildcard); e != nil && firstErr == nil {
				firstErr = e
			}
			continue
		}
		rt, ok := h.routeFor(d, routes, ups)
		if !ok {
			continue
		}
		path, _, e := h.Files.Write(rt)
		if e != nil {
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		if d.TraefikFile != path {
			d.TraefikFile = path
			_ = h.Store.Update(ctx, &d)
		}
	}
	return firstErr
}

// Reconcile derives ALL router files from the database and deletes orphans.
func (h *Handler) Reconcile(ctx context.Context) (traefikcfg.ReconcileResult, error) {
	doms, err := h.Store.ListRouted(ctx)
	if err != nil {
		return traefikcfg.ReconcileResult{}, err
	}
	routes, err := h.Store.ListRoutes(ctx)
	if err != nil {
		return traefikcfg.ReconcileResult{}, err
	}
	ups, err := h.Store.ListUpstreams(ctx)
	if err != nil {
		return traefikcfg.ReconcileResult{}, err
	}
	var desired []traefikcfg.Route
	for _, d := range doms {
		if rt, ok := h.routeFor(d, routes, ups); ok {
			desired = append(desired, rt)
		}
	}
	return h.Files.Reconcile(desired)
}

// ReverifyAll re-checks every verified custom domain; domains whose DNS no longer validates on
// two consecutive runs are downgraded (router + certificate removed).
func (h *Handler) ReverifyAll(ctx context.Context) (checked, downgraded int) {
	list, err := h.Store.ListReverify(ctx)
	if err != nil {
		h.log().Error("reverify list", "err", err)
		return 0, 0
	}
	for i := range list {
		if ctx.Err() != nil {
			return
		}
		d := list[i]
		checked++
		res := h.Verifier.Verify(ctx, d.Hostname, d.VerificationToken, d.VerificationType)
		now := time.Now().UTC()
		d.DNSLastChecked = &now
		if res.OK {
			d.DNSError, d.DNSFailCount = "", 0
			_ = h.Store.Update(ctx, &d)
			continue
		}
		d.DNSFailCount++
		d.DNSError = truncate(res.Detail, 500)
		if d.DNSFailCount < 2 {
			_ = h.Store.Update(ctx, &d)
			continue
		}
		d.Status = store.StatusFailed
		d.TraefikFile = ""
		if err := h.Store.Update(ctx, &d); err != nil {
			h.log().Error("reverify downgrade", "domain", d.ID, "err", err)
			continue
		}
		_ = h.Files.Remove(d.Hostname, d.Wildcard)
		if h.Certs != nil {
			_ = h.Certs.DeleteByDomain(ctx, d.ID)
		}
		downgraded++
		h.publish(ctx, "domain.unverified", "", d.OrgID, map[string]any{
			"domain_id": d.ID, "hostname": d.Hostname, "project_id": d.ProjectID, "reason": d.DNSError,
		})
	}
	return
}

func (h *Handler) registerCert(ctx context.Context, d *store.Domain) {
	if h.Certs == nil {
		return
	}
	id, err := h.Certs.Register(ctx, d.OrgID, d.ProjectID, d.ID, d.Hostname)
	if err != nil {
		h.log().Warn("certificate registration", "domain", d.ID, "err", err)
		return
	}
	if id != "" {
		d.CertificateID = &id
		_ = h.Store.Update(ctx, d)
	}
}

// ---------------------------------------------------------------- internal API

// InternalSetRoute switches the traefik service a project's domains route to.
func (h *Handler) InternalSetRoute(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	var req struct {
		Service   string   `json:"service"`
		Upstreams []string `json:"upstreams"`
	}
	if !validID(projectID) || httpx.Decode(r, &req) != nil || !serviceRe.MatchString(req.Service) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "valid projectId and service (jp-app-*|jp-prev-*) required")
		return
	}
	// Upstreams are validated strictly; omitting them (or sending []) clears any stored upstreams.
	ups, err := traefikcfg.ValidateUpstreams(req.Upstreams)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := h.Store.SetRoute(r.Context(), projectID, req.Service, ups); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to store route")
		return
	}
	if err := h.SyncProject(r.Context(), projectID); err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to rewrite routers: "+err.Error())
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"project_id": projectID, "service": req.Service, "upstreams": ups})
}

// SystemHost builds the <project>-<org>.<root> hostname (<=63 char label).
func (h *Handler) SystemHost(projectSlug, orgSlug, projectID string, collide bool) (string, error) {
	p, o := hostname.Slug(projectSlug), hostname.Slug(orgSlug)
	if p == "" || o == "" {
		return "", errors.New("project_slug and org_slug are required")
	}
	label := p + "-" + o
	if collide {
		label += "-" + ShortID(projectID)[:6]
	}
	if len(label) > 63 {
		sum := sha256.Sum256([]byte(label))
		label = strings.TrimRight(label[:54], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	host := label + "." + hostname.Normalize(h.Policy.RootDomain)
	if h.Policy.RootDomain == "" {
		return "", errors.New("ROOT_DOMAIN is not configured")
	}
	if err := h.Policy.ValidateSystem(host); err != nil {
		return "", err
	}
	return host, nil
}

// InternalSystemCreate registers <project-slug>-<org-slug>.<ROOT_DOMAIN> (idempotent).
func (h *Handler) InternalSystemCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID       string `json:"org_id"`
		ProjectID   string `json:"project_id"`
		ProjectSlug string `json:"project_slug"`
		OrgSlug     string `json:"org_slug"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validID(req.OrgID) || !validID(req.ProjectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id (uuid), project_slug, org_slug required")
		return
	}
	ctx := r.Context()
	existing, err := h.Store.FindSystemByProject(ctx, req.ProjectID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	var host string
	var d *store.Domain
	for attempt := 0; attempt < 2; attempt++ {
		host, err = h.SystemHost(req.ProjectSlug, req.OrgSlug, req.ProjectID, attempt == 1)
		if err != nil {
			httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
			return
		}
		if existing != nil && existing.Hostname == host {
			httpx.JSON(w, http.StatusOK, map[string]any{"domain": h.view(*existing, nil, false), "url": h.scheme() + host})
			return
		}
		now := time.Now().UTC()
		if existing != nil { // slugs changed: move the system hostname
			_ = h.Files.Remove(existing.Hostname, false)
			existing.Hostname = host
			existing.UpdatedAt = now
			if err = h.Store.Delete(ctx, existing.OrgID, existing.ProjectID, existing.ID); err != nil {
				httpx.Error(w, http.StatusInternalServerError, "internal", "failed to move system domain")
				return
			}
		}
		d = &store.Domain{
			OrgID: req.OrgID, ProjectID: req.ProjectID, Hostname: host, Kind: store.KindSystem,
			Status: store.StatusActive, VerificationType: "system", VerifiedMethod: "system", VerifiedAt: &now,
		}
		if err = h.Store.Create(ctx, d); err == nil {
			break
		}
		if !errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create system domain")
			return
		}
		existing = nil
	}
	if err != nil {
		httpx.Error(w, http.StatusConflict, "conflict", "system hostname already in use")
		return
	}
	if err := h.SyncProject(ctx, req.ProjectID); err != nil {
		h.log().Error("sync routers", "project", req.ProjectID, "err", err)
	}
	h.registerCert(ctx, d)
	h.publish(ctx, events.TypeDomainAdded, "", req.OrgID, map[string]any{"domain_id": d.ID, "hostname": d.Hostname, "project_id": req.ProjectID, "system": true})
	httpx.JSON(w, http.StatusCreated, map[string]any{"domain": h.view(*d, nil, false), "url": h.scheme() + host})
}

// InternalSystemDelete removes the system subdomain of a project.
func (h *Handler) InternalSystemDelete(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	d, err := h.Store.FindSystemByProject(r.Context(), projectID)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	_ = h.Files.Remove(d.Hostname, false)
	if err := h.Store.DeleteByID(r.Context(), d.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusInternalServerError, "internal", "delete failed")
		return
	}
	if h.Certs != nil {
		_ = h.Certs.DeleteByDomain(r.Context(), d.ID)
	}
	w.WriteHeader(http.StatusNoContent)
}

// InternalDeleteProject is the project-delete cascade.
func (h *Handler) InternalDeleteProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("projectId")
	if !validID(projectID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid project id")
		return
	}
	removed, err := h.Store.DeleteByProject(r.Context(), projectID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to delete domains")
		return
	}
	var failures []string
	for _, d := range removed {
		if err := h.Files.Remove(d.Hostname, d.Wildcard); err != nil {
			failures = append(failures, fmt.Sprintf("router %s: %v", d.ID, err))
		}
	}
	if h.Certs != nil {
		if err := h.Certs.DeleteByProject(r.Context(), projectID); err != nil {
			failures = append(failures, "certificates: "+err.Error())
		}
	}
	if len(failures) > 0 {
		httpx.Error(w, http.StatusBadGateway, "partial_failure", strings.Join(failures, "; "))
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": len(removed), "project_id": projectID})
}

// InternalPreviewProvision creates (or replaces) the preview hostname of a deployment.
func (h *Handler) InternalPreviewProvision(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrgID        string `json:"org_id"`
		ProjectID    string `json:"project_id"`
		DeploymentID string `json:"deployment_id"`
		Hostname     string `json:"hostname"`
	}
	if err := httpx.Decode(r, &req); err != nil || !validID(req.OrgID) || !validID(req.ProjectID) || !validID(req.DeploymentID) || req.Hostname == "" {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "org_id, project_id, deployment_id (uuids) and hostname required")
		return
	}
	host := hostname.Normalize(req.Hostname)
	wildcard, err := h.Policy.ValidatePreview(host)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if wildcard {
		host = strings.TrimPrefix(host, "*.")
	}
	ctx := r.Context()

	// Replace previous domains of this deployment.
	if existing, err := h.Store.FindByDeploymentID(ctx, req.DeploymentID); err == nil {
		for _, d := range existing {
			_ = h.Files.Remove(d.Hostname, d.Wildcard)
			_ = h.Store.DeleteByID(ctx, d.ID)
		}
	}
	now := time.Now().UTC()
	svc := PreviewService(req.DeploymentID)
	dep := req.DeploymentID
	d := &store.Domain{
		OrgID: req.OrgID, ProjectID: req.ProjectID, DeploymentID: &dep, Hostname: host, Wildcard: wildcard,
		Kind: store.KindPreview, Status: store.StatusActive, VerificationType: "preview", VerifiedMethod: "preview",
		VerifiedAt: &now, Service: &svc,
	}
	if err := h.Store.Create(ctx, d); err != nil {
		if errors.Is(err, store.ErrConflict) {
			httpx.Error(w, http.StatusConflict, "conflict", "preview hostname already in use")
			return
		}
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to create preview domain")
		return
	}
	rt, _ := h.routeFor(*d, nil, nil)
	if path, _, err := h.Files.Write(rt); err != nil {
		_ = h.Store.DeleteByID(ctx, d.ID)
		httpx.Error(w, http.StatusInternalServerError, "internal", "failed to write router: "+err.Error())
		return
	} else {
		d.TraefikFile = path
		_ = h.Store.Update(ctx, d)
	}
	scheme := "http://"
	if h.PreviewTLS && !wildcard {
		scheme = "https://"
	}
	h.publish(ctx, events.TypeDomainVerified, "", req.OrgID, map[string]any{
		"domain_id": d.ID, "hostname": d.Hostname, "project_id": req.ProjectID, "preview": true,
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"domain": h.view(*d, nil, false), "url": scheme + host, "preview": true})
}

func (h *Handler) InternalDeleteByDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID := r.PathValue("deploymentId")
	if !validID(deploymentID) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "deploymentId must be a uuid")
		return
	}
	list, err := h.Store.FindByDeploymentID(r.Context(), deploymentID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	removed := 0
	for _, d := range list {
		if d.Kind != store.KindPreview {
			continue
		}
		if err := h.Files.Remove(d.Hostname, d.Wildcard); err != nil {
			h.log().Warn("remove preview router", "domain", d.ID, "err", err)
		}
		if err := h.Store.DeleteByID(r.Context(), d.ID); err == nil {
			removed++
		}
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"removed": removed, "deployment_id": deploymentID})
}

// ---------------------------------------------------------------- helpers

func (h *Handler) publish(ctx context.Context, typ, actor, org string, payload map[string]any) {
	if h.Redis == nil {
		return
	}
	env := events.New(events.TopicDomain, typ, actor, org, payload)
	if _, err := events.PublishJSON(ctx, h.Redis, events.TopicDomain, env); err != nil {
		h.log().Warn("publish event", "type", typ, "err", err)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// InternalReissue makes Traefik retry ACME for a domain whose certificate is missing or failing:
// the router file is removed, then rewritten after ReissueGap (longer than Traefik's provider
// throttle so the change is not coalesced away). Only routed domains qualify.
func (h *Handler) InternalReissue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("domainId")
	if !validID(id) {
		httpx.Error(w, http.StatusBadRequest, "bad_request", "invalid domain id")
		return
	}
	d, err := h.Store.GetByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		httpx.Error(w, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal", "lookup failed")
		return
	}
	if !store.Routed(d.Status) {
		httpx.Error(w, http.StatusConflict, "not_routed", "domain is not verified; unverified domains never get certificates")
		return
	}
	h.mu.Lock()
	if h.reissueSem == nil {
		h.reissueSem = make(chan struct{}, 4)
	}
	sem := h.reissueSem
	h.mu.Unlock()
	select {
	case sem <- struct{}{}:
	default:
		httpx.Error(w, http.StatusTooManyRequests, "busy", "too many reissues in progress")
		return
	}
	gap := h.ReissueGap
	if gap <= 0 {
		gap = 3 * time.Second
	}
	dom := *d
	go func() {
		defer func() { <-sem }()
		if err := h.Files.Remove(dom.Hostname, dom.Wildcard); err != nil {
			h.log().Error("reissue remove", "domain", dom.ID, "err", err)
		}
		time.Sleep(gap)
		// rewrite from current DB state (the domain may have been deleted meanwhile)
		cur, err := h.Store.GetByID(context.Background(), dom.ID)
		if err != nil || !store.Routed(cur.Status) {
			return
		}
		if err := h.SyncProject(context.Background(), cur.ProjectID); err != nil {
			h.log().Error("reissue rewrite", "domain", dom.ID, "err", err)
		}
	}()
	httpx.JSON(w, http.StatusAccepted, map[string]any{"domain_id": id, "reissue": "scheduled"})
}
