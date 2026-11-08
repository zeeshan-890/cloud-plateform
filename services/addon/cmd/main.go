package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/addon/internal/catalog"
	"github.com/jp-cloud/addon/internal/clients"
	"github.com/jp-cloud/addon/internal/handlers"
	"github.com/jp-cloud/addon/internal/service"
	"github.com/jp-cloud/addon/internal/store"
	"github.com/jp-cloud/events"
	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8021")
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

	// Operator-controlled drop-in catalog (start-up only; built-ins cannot be overridden).
	if dir := os.Getenv("ADDON_CATALOG_DIR"); dir != "" {
		n, err := catalog.LoadCatalogDir(dir)
		if err != nil {
			log.Warn("addon catalog dir", "loaded", n, "err", err)
		} else {
			log.Info("addon catalog dir loaded", "dir", dir, "entries", n)
		}
	}
	total, prov := catalog.Counts()
	log.Info("addon catalog", "total", total, "provisionable", prov)

	svc := &service.Service{
		Store:   store.New(pool),
		Runtime: clients.NewRuntime(cfg.RuntimeURL),
		Secrets: clients.NewSecrets(cfg.SecretURL),
		Storage: clients.NewStorage(cfg.StorageURL),
		Log:     log,
		Publish: func(c context.Context, typ, actor, org string, payload map[string]any) {
			env := events.New(events.TopicAddon, typ, actor, org, payload)
			if _, err := events.PublishJSON(c, rdb, events.TopicAddon, env); err != nil {
				log.Warn("publish event", "type", typ, "err", err)
			}
		},
	}
	if n := svc.Sweep(ctx); n > 0 {
		log.Warn("cleaned interrupted provisioning", "count", n)
	}
	h := &handlers.Handler{Svc: svc, JWT: jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz: membership.New(cfg.OrganizationURL, cfg.ProjectURL)}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	// provisioning and backups are long requests: generous write timeout, bounded read timeouts
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 45 * time.Minute, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("addon listening", "port", cfg.HTTPPort)
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
