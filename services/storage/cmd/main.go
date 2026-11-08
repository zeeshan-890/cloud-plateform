package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/storage/internal/handlers"
	"github.com/jp-cloud/storage/internal/miniox"
	"github.com/jp-cloud/storage/internal/store"
)

func envMB(key string, def int64) int64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return def
}

func main() {
	cfg, err := config.Load("8017")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcfg, err := miniox.ConfigFromEnv()
	if err != nil {
		log.Error("storage configuration", "err", err)
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
	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	// No simulate fallback: an unreachable MinIO is a startup failure, not a silent in-memory store.
	backend, err := miniox.NewMinIO(ctx, mcfg)
	if err != nil {
		log.Error("object store", "err", err)
		os.Exit(1)
	}

	h := &handlers.Handler{
		Store: store.New(pool), Backend: backend, Redis: rdb, Log: log,
		JWT:           jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz:         membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		MaxJSONUpload: envMB("STORAGE_MAX_UPLOAD_MB", 16) << 20,
		MaxObject:     envMB("STORAGE_MAX_OBJECT_MB", 2048) << 20,
		QuotaBytes:    envMB("STORAGE_QUOTA_MB", 5120) << 20,
		PublicURL:     os.Getenv("STORAGE_PUBLIC_URL"),
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	// uploads/downloads are streamed: no overall write/read deadline per request beyond generous bounds
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Minute, WriteTimeout: 30 * time.Minute, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("storage listening", "port", cfg.HTTPPort, "backend", backend.Name(), "quota_mb", h.QuotaBytes>>20)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	sctx, c2 := context.WithTimeout(context.Background(), 20*time.Second)
	defer c2()
	_ = srv.Shutdown(sctx)
}
