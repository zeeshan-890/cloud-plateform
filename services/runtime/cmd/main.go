package main

import (
	"context"
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
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/runtime/internal/clients"
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/handlers"
	"github.com/jp-cloud/runtime/internal/manager"
	"github.com/jp-cloud/runtime/internal/store"
)

func main() {
	cfg, err := config.Load("8010")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// RUNTIME_MODE: "docker" (default) or an explicit "simulate" (dev only, refused in production).
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("RUNTIME_MODE")))
	if mode == "" {
		mode = dockerx.ModeDocker
	}
	if mode != dockerx.ModeDocker && mode != dockerx.ModeSimulate {
		log.Error("invalid RUNTIME_MODE", "mode", mode)
		os.Exit(1)
	}
	if mode == dockerx.ModeSimulate && strings.EqualFold(cfg.Env, "production") {
		log.Error("RUNTIME_MODE=simulate is not allowed when JP_ENV=production")
		os.Exit(1)
	}

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

	engine := dockerx.New(mode)
	if err := engine.Ping(ctx); err != nil {
		// Not fatal at boot (compose ordering) but every container operation will fail loudly.
		log.Error("docker engine unreachable; container operations will return errors", "err", err)
	}
	st := store.New(pool)
	mgrCfg := manager.ConfigFromEnv()
	mgr := manager.New(ctx, mgrCfg, engine, st, clients.NewSecrets(cfg.SecretURL), clients.NewRoutes(cfg.DomainURL), log)
	if mgrCfg.ProberContainer != "" && !engine.Simulated() {
		ep := manager.ExecProber{Engine: engine, Container: mgrCfg.ProberContainer}
		mgr.Prober = ep
		manager.TCPProbe = ep.TCP
		log.Info("health probes run in sidecar", "container", mgrCfg.ProberContainer)
	}

	// multi-server placement: node agent tokens are sealed with a key derived from the platform secret
	mgr.Nodes = st
	mgr.NodeKey = secretbox.DeriveKey(cfg.JWTSecret, "runtime-nodes")

	h := &handlers.Handler{
		Nodes:   st,
		NodeKey: mgr.NodeKey,
		Store:   st,
		JWT:     jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz:   membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		Manager: mgr,
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	// override the plain healthz with an honest one
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := engine.Ping(c); err != nil {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded", "mode": engine.Mode(), "docker": "unreachable"})
			return
		}
		httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok", "mode": engine.Mode(), "docker": "ok"})
	})
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{
		Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		// WriteTimeout stays unset: deploys are synchronous and log streams are long-lived; both are bounded in code.
	}
	go func() {
		log.Info("runtime listening", "port", cfg.HTTPPort, "mode", engine.Mode())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	reconcileEvery := 60 * time.Second
	if v := os.Getenv("RUNTIME_RECONCILE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			reconcileEvery = d
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		mgr.RunReconcile(ctx, reconcileEvery)
	}()

	sampleEvery := 30 * time.Second
	if v := os.Getenv("METRICS_SAMPLE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			sampleEvery = d
		}
	}
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		mgr.RunSampler(ctx, sampleEvery, clients.NewMetrics(cfg.MetricsURL))
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	<-done
	<-sampleDone
	mgr.Wait()
}
