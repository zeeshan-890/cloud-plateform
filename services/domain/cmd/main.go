package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/domain/internal/handlers"
	"github.com/jp-cloud/domain/internal/store"
	"github.com/jp-cloud/domain/internal/traefikcfg"
	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8012")
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

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	dnsStub := os.Getenv("DOMAIN_DNS_STUB")
	stub := dnsStub == "" || dnsStub == "true" || dnsStub == "1"

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{
		Store:           store.New(pool),
		JWT:             jm,
		OrganizationURL: cfg.OrganizationURL,
		CertificateURL:  cfg.CertificateURL,
		Traefik:         traefikcfg.New(os.Getenv("TRAEFIK_DYNAMIC_DIR"), os.Getenv("TRAEFIK_BACKEND_URL"), os.Getenv("TRAEFIK_CERT_RESOLVER")),
		Redis:           rdb,
		HTTP:            &http.Client{Timeout: 8 * time.Second},
		DNSStub:         stub,
		CNAMETarget:     os.Getenv("DOMAIN_CNAME_TARGET"),
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("domain listening", "port", cfg.HTTPPort, "dns_stub", stub)
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
