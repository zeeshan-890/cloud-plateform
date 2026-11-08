package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jp-cloud/certificate/internal/handlers"
	"github.com/jp-cloud/certificate/internal/prober"
	"github.com/jp-cloud/certificate/internal/store"
	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8013")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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

	tlsOn := config.GetEnvBool("TLS_ENABLED", false)
	probeAddr := os.Getenv("TLS_PROBE_ADDR")
	if probeAddr == "" {
		probeAddr = "traefik:443"
	}
	resolver := os.Getenv("TRAEFIK_CERT_RESOLVER")
	if resolver == "" {
		resolver = "letsencrypt"
	}
	h := &handlers.Handler{
		Store:      store.New(pool),
		JWT:        jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz:      membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		Prober:     &prober.Prober{Addr: probeAddr, Timeout: 6 * time.Second, Staging: config.GetEnvBool("ACME_STAGING", false)},
		Reissuer:   &handlers.DomainReissuer{BaseURL: cfg.DomainURL, HTTP: internalauth.NewClient(10 * time.Second)},
		Redis:      rdb,
		Log:        log,
		TLSEnabled: tlsOn,
		Resolver:   resolver,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // periodic reality scan
		defer wg.Done()
		t := time.NewTicker(2 * time.Minute)
		defer t.Stop()
		h.Scan(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				h.Scan(ctx)
			}
		}
	}()

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("certificate listening", "port", cfg.HTTPPort, "tls_enabled", tlsOn, "probe", probeAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	sctx, c2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer c2()
	_ = srv.Shutdown(sctx)
	wg.Wait()
}
