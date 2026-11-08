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
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/repository/internal/githubapp"
	"github.com/jp-cloud/repository/internal/handlers"
	"github.com/jp-cloud/repository/internal/secbox"
	"github.com/jp-cloud/repository/internal/store"
)

func main() {
	cfg, err := config.Load("8005")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	box, err := secbox.NewFromEnv()
	if err != nil {
		log.Error("credential encryption key rejected", "err", err)
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
	st := store.New(pool, box)
	if n, err := st.ScrubLegacy(ctx); err != nil {
		log.Error("legacy credential scrub failed", "err", err)
		os.Exit(1)
	} else if n > 0 {
		log.Info("moved legacy credentials into encrypted columns", "rows", n)
	}

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis is required (install state, webhook replay protection)", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	ghHTTP := &http.Client{Timeout: 15 * time.Second}
	var gh *githubapp.Client
	if c, err := githubapp.NewFromEnv(ghHTTP); err != nil {
		log.Info("github app not configured (token / deploy key / public repositories only)", "reason", err)
	} else {
		gh = c
		log.Info("github app configured", "app_id", c.AppID, "slug", c.Slug)
	}
	stub := strings.EqualFold(os.Getenv("GITHUB_MODE"), "stub")
	if stub && strings.EqualFold(cfg.Env, "production") {
		log.Error("GITHUB_MODE=stub is not allowed when JP_ENV=production")
		os.Exit(1)
	}
	h := &handlers.Handler{
		Store: st, JWT: jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz:               membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		DeploymentURL:       cfg.DeploymentURL,
		GlobalWebhookSecret: os.Getenv("GITHUB_WEBHOOK_SECRET"),
		PublicBaseURL:       cfg.PublicBaseURL,
		DashboardURL:        os.Getenv("DASHBOARD_URL"),
		HTTP:                ghHTTP,
		Internal:            internalauth.NewClient(20 * time.Second),
		Redis:               rdb,
		GitHub:              gh,
		StubMode:            stub,
		Log:                 log,
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("repository listening", "port", cfg.HTTPPort, "stub", stub && gh == nil)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	<-ctx.Done()
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}
