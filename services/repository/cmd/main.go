package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/repository/internal/githubapp"
	"github.com/jp-cloud/repository/internal/handlers"
	"github.com/jp-cloud/repository/internal/store"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfg, err := config.Load("8005")
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

	var redisClient *redis.Client
	if c, err := redisx.Connect(ctx, cfg.RedisURL); err != nil {
		log.Info("redis unavailable; events disabled", "err", err)
	} else {
		redisClient = c
		defer redisClient.Close()
	}

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	httpClient := &http.Client{Timeout: 15 * time.Second}
	var ghClient *githubapp.Client
	if c, err := githubapp.NewFromEnv(httpClient); err != nil {
		log.Info("github app not configured; install/status stub mode", "err", err)
	} else {
		ghClient = c
		log.Info("github app configured", "app_id", c.AppID, "slug", c.Slug)
	}
	dashboardURL := os.Getenv("DASHBOARD_URL")
	if dashboardURL == "" {
		dashboardURL = "http://localhost:3000"
	}
	h := &handlers.Handler{
		Store:           store.New(pool),
		JWT:             jm,
		OrganizationURL: cfg.OrganizationURL,
		DeploymentURL:   cfg.DeploymentURL,
		WebhookSecret:   cfg.GitHubWebhookSecret,
		PublicBaseURL:   cfg.PublicBaseURL,
		DashboardURL:    dashboardURL,
		HTTP:            httpClient,
		Redis:           redisClient,
		GitHub:          ghClient,
		Log:             log,
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("repository listening", "port", cfg.HTTPPort)
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
