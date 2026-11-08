package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/registry/internal/handlers"
	"github.com/jp-cloud/registry/internal/regclient"
	"github.com/jp-cloud/registry/internal/store"
)

func main() {
	cfg, err := config.Load("8009")
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

	// REGISTRY_API_URL: where registry:2 listens for the HTTP API (it must run with
	// REGISTRY_STORAGE_DELETE_ENABLED=true); REGISTRY_REF_HOSTS: hosts used in image refs.
	rc := regclient.New(getenv("REGISTRY_API_URL", "http://registry:5000"), refHosts()...)
	rc.User, rc.Password = os.Getenv("REGISTRY_USER"), os.Getenv("REGISTRY_PASSWORD")
	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{
		Store:    store.New(pool),
		JWT:      jm,
		Authz:    membership.New(cfg.OrganizationURL, cfg.ProjectURL),
		Registry: rc,
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 5 * time.Minute, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("registry listening", "port", cfg.HTTPPort)
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

func refHosts() []string {
	var hosts []string
	for _, h := range strings.Split(os.Getenv("REGISTRY_REF_HOSTS"), ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}
	if h := strings.TrimSpace(os.Getenv("REGISTRY_HOST")); h != "" {
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		hosts = []string{"registry:5000", "127.0.0.1:5000", "localhost:5000"}
	}
	return hosts
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
