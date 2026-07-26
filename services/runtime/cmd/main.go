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
	"github.com/jp-cloud/runtime/internal/dockerx"
	"github.com/jp-cloud/runtime/internal/handlers"
	"github.com/jp-cloud/runtime/internal/store"
)

func main() {
	cfg, err := config.Load("8010")
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

	mode := os.Getenv("RUNTIME_MODE")
	if mode == "" {
		mode = "simulate"
	}
	engine := dockerx.New(mode)
	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{
		Store:           store.New(pool),
		JWT:             jm,
		OrganizationURL: cfg.OrganizationURL,
		Engine:          engine,
		HTTP:            &http.Client{Timeout: 5 * time.Second},
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("runtime listening", "port", cfg.HTTPPort, "mode", engine.EffectiveMode(context.Background()))
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
