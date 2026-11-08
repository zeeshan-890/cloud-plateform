package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/project/internal/handlers"
	"github.com/jp-cloud/project/internal/store"
)

func main() {
	cfg, err := config.Load("8003")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx := context.Background()

	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	migDir := os.Getenv("MIGRATIONS_DIR")
	if migDir == "" {
		migDir = "migrations"
	}
	if err := postgres.Migrate(ctx, pool, migDir); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	// Project binding is checked against ourselves (GET /internal/orgs/{o}/projects/{p}); the
	// membership client calls over HTTP like every other service.
	members := membership.New(cfg.OrganizationURL, "")
	h := &handlers.Handler{
		Store:           store.New(pool),
		JWT:             jm,
		Members:         members,
		HTTP:            internalauth.NewClient(30 * time.Second),
		Log:             log,
		OrganizationURL: cfg.OrganizationURL,
		DomainURL:       cfg.DomainURL,
		// Order matters: add-ons/databases first (they call runtime), runtime next, secrets/logs last.
		Cleanup: []handlers.CleanupTarget{
			{Name: "addon", URL: cfg.AddonURL},
			{Name: "database", URL: cfg.DatabaseURLSvc},
			{Name: "runtime", URL: cfg.RuntimeURL},
			{Name: "domain", URL: cfg.DomainURL},
			{Name: "certificate", URL: cfg.CertificateURL},
			{Name: "storage", URL: cfg.StorageURL},
			{Name: "deployment", URL: cfg.DeploymentURL},
			{Name: "build", URL: cfg.BuildURL},
			{Name: "repository", URL: cfg.RepositoryURL},
			{Name: "scheduler", URL: cfg.SchedulerURL},
			{Name: "registry", URL: cfg.RegistryURL},
			{Name: "secret", URL: cfg.SecretURL},
			{Name: "logging", URL: cfg.LoggingURL},
			{Name: "metrics", URL: cfg.MetricsURL},
			{Name: "organization", URL: cfg.OrganizationURL},
		},
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute, // delete cascades through ~14 services
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		log.Info("project listening", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
