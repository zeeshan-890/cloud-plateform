package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/otelx"
	"github.com/jp-cloud/gateway/internal/proxy"
)

func main() {
	cfg, err := config.Load("8000")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)

	identity, err := proxy.New(cfg.IdentityURL)
	if err != nil {
		log.Error("identity url", "err", err)
		os.Exit(1)
	}
	organization, err := proxy.New(cfg.OrganizationURL)
	if err != nil {
		log.Error("organization url", "err", err)
		os.Exit(1)
	}
	project, err := proxy.New(cfg.ProjectURL)
	if err != nil {
		log.Error("project url", "err", err)
		os.Exit(1)
	}
	repository, err := proxy.New(cfg.RepositoryURL)
	if err != nil {
		log.Error("repository url", "err", err)
		os.Exit(1)
	}
	deployment, err := proxy.New(cfg.DeploymentURL)
	if err != nil {
		log.Error("deployment url", "err", err)
		os.Exit(1)
	}
	buildSvc, err := proxy.New(cfg.BuildURL)
	if err != nil {
		log.Error("build url", "err", err)
		os.Exit(1)
	}
	registryAPI, err := proxy.New(cfg.RegistryURL)
	if err != nil {
		log.Error("registry url", "err", err)
		os.Exit(1)
	}
	runtime, err := proxy.New(cfg.RuntimeURL)
	if err != nil {
		log.Error("runtime url", "err", err)
		os.Exit(1)
	}
	domain, err := proxy.New(cfg.DomainURL)
	if err != nil {
		log.Error("domain url", "err", err)
		os.Exit(1)
	}
	certificate, err := proxy.New(cfg.CertificateURL)
	if err != nil {
		log.Error("certificate url", "err", err)
		os.Exit(1)
	}
	secretSvc, err := proxy.New(cfg.SecretURL)
	if err != nil {
		log.Error("secret url", "err", err)
		os.Exit(1)
	}
	loggingSvc, err := proxy.New(cfg.LoggingURL)
	if err != nil {
		log.Error("logging url", "err", err)
		os.Exit(1)
	}
	metricsSvc, err := proxy.New(cfg.MetricsURL)
	if err != nil {
		log.Error("metrics url", "err", err)
		os.Exit(1)
	}
	storageSvc, err := proxy.New(cfg.StorageURL)
	if err != nil {
		log.Error("storage url", "err", err)
		os.Exit(1)
	}
	databaseSvc, err := proxy.New(cfg.DatabaseURLSvc)
	if err != nil {
		log.Error("database url", "err", err)
		os.Exit(1)
	}
	schedulerSvc, err := proxy.New(cfg.SchedulerURL)
	if err != nil {
		log.Error("scheduler url", "err", err)
		os.Exit(1)
	}
	aiSvc, err := proxy.New(cfg.AIURL)
	if err != nil {
		log.Error("ai url", "err", err)
		os.Exit(1)
	}
	billingSvc, err := proxy.New(cfg.BillingURL)
	if err != nil {
		log.Error("billing url", "err", err)
		os.Exit(1)
	}

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	httpClient := &http.Client{Timeout: 5 * time.Second}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)

	// Public auth routes (no JWT) — strip /api/v1 prefix when forwarding
	mux.HandleFunc("POST /api/v1/auth/register", stripAndProxy(identity, "/api/v1"))
	mux.HandleFunc("POST /api/v1/auth/login", stripAndProxy(identity, "/api/v1"))
	mux.HandleFunc("POST /api/v1/auth/refresh", stripAndProxy(identity, "/api/v1"))

	// Public GitHub webhook (HMAC verified upstream)
	mux.HandleFunc("POST /api/v1/webhooks/github", stripAndProxy(repository, "/api/v1"))

	auth := bearerOrPAT(jm, httpClient, cfg.IdentityURL)

	// Authenticated auth routes
	for _, pattern := range []string{
		"POST /api/v1/auth/logout",
		"GET /api/v1/auth/me",
		"GET /api/v1/auth/sessions",
		"DELETE /api/v1/auth/sessions/{id}",
		"POST /api/v1/auth/pats",
		"GET /api/v1/auth/pats",
		"DELETE /api/v1/auth/pats/{id}",
	} {
		mux.Handle(pattern, auth(http.HandlerFunc(injectUser(stripAndProxy(identity, "/api/v1")))))
	}

	// Org routes
	mux.Handle("POST /api/v1/orgs", auth(http.HandlerFunc(injectUser(stripAndProxy(organization, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs", auth(http.HandlerFunc(injectUser(stripAndProxy(organization, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/invites/accept", auth(http.HandlerFunc(injectUser(stripAndProxy(organization, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(organization, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/invites", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(organization, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/members", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(organization, "/api/v1")))))

	// Project routes (org-scoped)
	mux.Handle("POST /api/v1/orgs/{orgId}/projects", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("PATCH /api/v1/orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("PUT /api/v1/orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/config", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/config/drift", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(project, "/api/v1")))))

	// Phase 2 — Git connect
	mux.Handle("POST /api/v1/orgs/{orgId}/github/install/start", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/github/install/callback", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/github/installations", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/github/repos", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/repos", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/repos", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/repos/{repoId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(repository, "/api/v1")))))

	// Phase 2 — Deployments
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/deployments", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(deployment, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/deployments", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(deployment, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(deployment, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/deployments/rollback", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(deployment, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}/rollback", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(deployment, "/api/v1")))))

	// Phase 3 — Builds
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/builds", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(buildSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/builds/{buildId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(buildSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/builds/{buildId}/logs", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(buildSvc, "/api/v1")))))
  mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/images", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(registryAPI, "/api/v1")))))

	// Phase 4 — Runtime
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/runtime/instances", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/runtime/instances", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/start", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/stop", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/runtime/containers", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(runtime, "/api/v1")))))

	// Phase 4 — Domains
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(domain, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/domains", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(domain, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(domain, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/domains/{domainId}/verify", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(domain, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/domains/{domainId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(domain, "/api/v1")))))

	// Phase 4 — Certificates
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/certificates", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(certificate, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/certificates/{certId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(certificate, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/certificates/{certId}/renew", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(certificate, "/api/v1")))))

	// Phase 5 — Secrets & environments
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/environments", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/environments", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("PUT /api/v1/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/env/{env}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("PUT /api/v1/orgs/{orgId}/projects/{projectId}/env/{env}/{name}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/env/{env}/{name}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(secretSvc, "/api/v1")))))

	// Phase 5 — Logs
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(loggingSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/logs", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(loggingSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/logs/ingest", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(loggingSvc, "/api/v1")))))

	// Phase 5 — Metrics
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(metricsSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/metrics", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(metricsSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/metrics/targets", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(metricsSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/metrics/targets", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(metricsSvc, "/api/v1")))))

	// Phase 6 — Storage
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/storage/bucket", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/storage/bucket", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/storage/signed-url", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/storage/objects", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(storageSvc, "/api/v1")))))

	// Phase 6 — Databases
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/databases", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(databaseSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/databases", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(databaseSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/databases/{dbId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(databaseSvc, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/databases/{dbId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(databaseSvc, "/api/v1")))))

	// Phase 6 — Cron / queues (scheduler)
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(schedulerSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/cron", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(schedulerSvc, "/api/v1")))))
	mux.Handle("DELETE /api/v1/orgs/{orgId}/projects/{projectId}/cron/{cronId}", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(schedulerSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/queues", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(schedulerSvc, "/api/v1")))))

	// Phase 7 — AI ops
	mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/ai/explain", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(aiSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/ai/ask", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(aiSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/ai/status", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(aiSvc, "/api/v1")))))

	// Phase 7 — Billing
	mux.Handle("GET /api/v1/billing/plans", auth(http.HandlerFunc(injectUser(stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/billing/plans", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/billing/usage", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/billing/events", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("POST /api/v1/orgs/{orgId}/billing/events", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("GET /api/v1/orgs/{orgId}/billing/plan", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))
	mux.Handle("PUT /api/v1/orgs/{orgId}/billing/plan", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(billingSvc, "/api/v1")))))

	otelCfg := otelx.FromEnv("gateway")
	handler := middleware.Chain(mux, otelx.Propagate(otelCfg), middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("gateway listening", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}

// bearerOrPAT accepts JWT access tokens or jp_pat_* personal access tokens.
func bearerOrPAT(jm *jwtutil.Manager, client *http.Client, identityURL string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			if h == "" || !strings.HasPrefix(strings.ToLower(h), "bearer ") {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			token := strings.TrimSpace(h[7:])

			if strings.HasPrefix(token, "jp_pat_") {
				claims, ok := verifyPAT(r, client, identityURL, token)
				if !ok {
					httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
					return
				}
				// Downstream services expect JWT — mint a short-lived access token.
				access, _, err := jm.IssueAccess(claims.UserID, claims.Email, claims.Name)
				if err != nil {
					httpx.Error(w, http.StatusInternalServerError, "internal", "failed to issue access token")
					return
				}
				r.Header.Set("Authorization", "Bearer "+access)
				ctx := r.Context()
				ctx = context.WithValue(ctx, middleware.ClaimsKey, claims)
				ctx = context.WithValue(ctx, middleware.UserIDKey, claims.UserID)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			claims, err := jm.Parse(token)
			if err != nil || claims.Type != "access" {
				httpx.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			ctx := r.Context()
			ctx = context.WithValue(ctx, middleware.ClaimsKey, claims)
			ctx = context.WithValue(ctx, middleware.UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func verifyPAT(r *http.Request, client *http.Client, identityURL, token string) (*jwtutil.Claims, bool) {
	if identityURL == "" || client == nil {
		return nil, false
	}
	body, _ := json.Marshal(map[string]string{"token": token})
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(identityURL, "/")+"/internal/pats/verify", strings.NewReader(string(body)))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var payload struct {
		User struct {
			ID    string `json:"id"`
			Email string `json:"email"`
			Name  string `json:"name"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil || payload.User.ID == "" {
		return nil, false
	}
	return &jwtutil.Claims{
		UserID: payload.User.ID,
		Email:  payload.User.Email,
		Name:   payload.User.Name,
		Type:   "access",
	}, true
}

func stripAndProxy(upstream http.Handler, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r2 := r.Clone(r.Context())
		r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		if r2.URL.Path == "" {
			r2.URL.Path = "/"
		}
		upstream.ServeHTTP(w, r2)
	}
}

func injectUser(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if claims, ok := middleware.ClaimsFrom(r.Context()); ok {
			r.Header.Set("X-User-ID", claims.UserID)
			r.Header.Set("X-User-Email", claims.Email)
		}
		next(w, r)
	}
}

func withOrgContext(_ *jwtutil.Manager, client *http.Client, orgURL string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.ClaimsFrom(r.Context())
		if !ok {
			httpx.Error(w, http.StatusUnauthorized, "unauthorized", "missing claims")
			return
		}
		r.Header.Set("X-User-ID", claims.UserID)
		r.Header.Set("X-User-Email", claims.Email)

		orgID := r.PathValue("orgId")
		if orgID != "" && orgURL != "" {
			url := strings.TrimRight(orgURL, "/") + "/internal/orgs/" + orgID + "/members/" + claims.UserID
			resp, err := client.Get(url)
			if err == nil {
				defer resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					var body struct {
						Role string `json:"role"`
					}
					_ = json.NewDecoder(resp.Body).Decode(&body)
					r.Header.Set("X-Org-ID", orgID)
					r.Header.Set("X-Org-Role", body.Role)
				}
			}
		}
		next(w, r)
	}
}
