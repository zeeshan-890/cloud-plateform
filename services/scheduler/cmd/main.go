package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/httpx"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/scheduler/internal/cron"
	"github.com/jp-cloud/scheduler/internal/handlers"
	"github.com/jp-cloud/scheduler/internal/loop"
)

func main() {
	cfg, err := config.Load("8011")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	runtimeURL := cfg.RuntimeURL
	if runtimeURL == "" {
		runtimeURL = "http://localhost:8010"
	}

	cronStore := cron.NewStore(rdb)
	sched := loop.New(loop.Config{
		Redis:         rdb,
		RuntimeURL:    runtimeURL,
		RegistryURL:   cfg.RegistryURL,
		DeploymentURL: cfg.DeploymentURL,
		Log:           log,
		Slot:          getenv("SCHEDULER_SLOT", "node-1"),
		HealthEvery:   20 * time.Second,
		CleanupEvery:  parseDurationEnv("CLEANUP_INTERVAL", time.Hour),
		Cron:          cronStore,
		PreviewTTL:    parseDurationEnv("PREVIEW_TTL", 72*time.Hour),
		ImageTTL:      parseDurationEnv("ORPHAN_IMAGE_TTL", 168*time.Hour),
	})

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{Cron: cronStore, JWT: jm, Slot: getenv("SCHEDULER_SLOT", "node-1")}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		httpx.JSON(w, http.StatusOK, map[string]any{
			"slot":             getenv("SCHEDULER_SLOT", "node-1"),
			"runtime_url":      runtimeURL,
			"consumer_group":   "jp-scheduler",
			"cleanup_group":    "jp-cleanup",
			"jobs_group":       "jp-jobs",
			"rolling_update":   "stub",
			"cleanup_interval": getenv("CLEANUP_INTERVAL", "1h"),
		})
	})
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("scheduler listening", "port", cfg.HTTPPort)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	go func() {
		if err := sched.Run(ctx); err != nil && err != context.Canceled {
			log.Error("scheduler loop", "err", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer c2()
	_ = srv.Shutdown(shutdownCtx)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseDurationEnv(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
