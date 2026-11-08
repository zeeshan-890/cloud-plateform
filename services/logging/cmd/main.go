package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/logging/internal/handlers"
	"github.com/jp-cloud/logging/internal/retention"
	"github.com/jp-cloud/logging/internal/store"
)

func main() {
	cfg, err := config.Load("8015")
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

	st := store.New(pool)
	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{
		Store:    st,
		JWT:      jm,
		Members:  membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		BuildURL: cfg.BuildURL,
		HTTP:     &http.Client{Timeout: 10 * time.Second},
		LokiURL:  os.Getenv("LOKI_URL"),
		Log:      log,
	}

	// Retention: LOG_RETENTION_DAYS (default 14).
	retDays, _ := strconv.Atoi(os.Getenv("LOG_RETENTION_DAYS"))
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); retention.Run(runCtx, st, retDays, time.Hour, log) }()

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{
		Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	go func() {
		log.Info("logging listening", "port", cfg.HTTPPort, "loki", h.LokiURL != "", "retention_days", retDays)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancelRun()
	wg.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
