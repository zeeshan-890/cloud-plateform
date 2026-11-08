package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jp-cloud/domain/internal/certclient"
	"github.com/jp-cloud/domain/internal/dnscheck"
	"github.com/jp-cloud/domain/internal/handlers"
	"github.com/jp-cloud/domain/internal/hostname"
	"github.com/jp-cloud/domain/internal/store"
	"github.com/jp-cloud/domain/internal/traefikcfg"
	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
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
	prod := strings.EqualFold(cfg.Env, "production")

	rootDomain := hostname.Normalize(os.Getenv("ROOT_DOMAIN"))
	if prod && rootDomain == "" {
		log.Error("ROOT_DOMAIN is required in production")
		os.Exit(1)
	}
	dynDir := os.Getenv("TRAEFIK_DYNAMIC_DIR")
	if dynDir == "" {
		if prod {
			log.Error("TRAEFIK_DYNAMIC_DIR is required in production")
			os.Exit(1)
		}
		dynDir = "/etc/traefik/dynamic"
	}

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

	verifier := dnscheck.FromEnv()
	if prod && verifier.CNAMETarget == "" && len(verifier.ATargets) == 0 {
		log.Warn("neither DOMAIN_CNAME_TARGET nor DOMAIN_A_TARGETS set: only TXT ownership verification is offered; customers get no routing instructions")
	}

	h := &handlers.Handler{
		Store: store.New(pool),
		JWT:   jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz: membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		Files: traefikcfg.New(dynDir, os.Getenv("TRAEFIK_CERT_RESOLVER")),
		Policy: hostname.Policy{
			RootDomain:        rootDomain,
			ManagementDomain:  hostname.Normalize(os.Getenv("MANAGEMENT_DOMAIN")),
			PreviewBase:       hostname.Normalize(os.Getenv("PREVIEW_BASE_DOMAIN")),
			AllowInternalTLDs: !prod || config.GetEnvBool("DOMAIN_ALLOW_INTERNAL_TLDS", false),
		},
		Verifier:    verifier,
		Certs:       certclient.New(cfg.CertificateURL),
		Redis:       rdb,
		Log:         log,
		TLSEnabled:  config.GetEnvBool("TLS_ENABLED", false),
		PreviewTLS:  config.GetEnvBool("PREVIEW_TLS", false),
		CNAMETarget: verifier.CNAMETarget,
		ATargets:    verifier.ATargets,
	}

	// Traefik config is always derived from the DB: reconcile before serving.
	if res, err := h.Reconcile(ctx); err != nil {
		log.Error("initial router reconcile failed", "err", err)
	} else {
		log.Info("routers reconciled", "written", res.Written, "unchanged", res.Unchanged, "removed", res.Removed, "errors", res.Errors)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // periodic self-heal of router files
		defer wg.Done()
		t := time.NewTicker(10 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if res, err := h.Reconcile(ctx); err != nil {
					log.Error("router reconcile", "err", err)
				} else if res.Written+res.Removed > 0 {
					log.Warn("router drift repaired", "written", res.Written, "removed", res.Removed)
				}
			}
		}
	}()
	go func() { // DNS re-verification every 6h
		defer wg.Done()
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				checked, down := h.ReverifyAll(ctx)
				log.Info("domain re-verification", "checked", checked, "downgraded", down)
			}
		}
	}()

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{
		Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
	}
	go func() {
		log.Info("domain listening", "port", cfg.HTTPPort, "tls", h.TLSEnabled, "root_domain", rootDomain)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	cancel()
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
	wg.Wait()
}
