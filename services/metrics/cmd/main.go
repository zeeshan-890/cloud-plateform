package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/metrics/internal/handlers"
	"github.com/jp-cloud/metrics/internal/store"
)

func main() {
	cfg, err := config.Load("8016")
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

	// Live is the default. Simulation is explicit, dev/test only and visible in every response.
	simulate := os.Getenv("METRICS_MODE") == "simulate"
	if simulate && !handlers.SimulateAllowed(cfg.Env) {
		log.Error("METRICS_MODE=simulate is not allowed in production")
		os.Exit(1)
	}

	st := store.New(pool)
	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{Store: st, JWT: jm, Members: membership.New(cfg.OrganizationURL, cfg.ProjectURL), Simulate: simulate}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		days := handlers.RetentionDays()
		for {
			for runCtx.Err() == nil {
				cctx, cancel := context.WithTimeout(runCtx, 30*time.Second)
				n, err := st.DeleteOlderThan(cctx, time.Now().AddDate(0, 0, -days), 5000)
				cancel()
				if err != nil {
					log.Warn("metrics retention failed", "err", err)
					break
				}
				if n < 5000 {
					break
				}
			}
			select {
			case <-runCtx.Done():
				return
			case <-t.C:
			}
		}
	}()

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{
		Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10,
	}
	go func() {
		log.Info("metrics listening", "port", cfg.HTTPPort, "mode", map[bool]string{true: "simulate", false: "live"}[simulate])
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
