package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jp-cloud/database/internal/addonclient"
	"github.com/jp-cloud/database/internal/handlers"
	"github.com/jp-cloud/database/internal/store"
	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8018")
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

	st := store.New(pool)
	// The old implementation granted tenants LOGIN roles on the platform database. Cut that access.
	if roles, err := st.LegacyRoles(ctx); err == nil {
		for _, r := range roles {
			q := fmt.Sprintf(`ALTER ROLE "%s" NOLOGIN`, strings.ReplaceAll(r, `"`, ``))
			if _, err := pool.Exec(ctx, q); err != nil {
				log.Warn("could not disable legacy tenant role", "role", r, "err", err)
			} else {
				log.Warn("legacy tenant role disabled (NOLOGIN)", "role", r)
			}
		}
	}

	h := &handlers.Handler{
		Store: st, Addons: addonclient.New(cfg.AddonURL), Redis: rdb, Log: log,
		JWT:   jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Authz: membership.New(cfg.OrganizationURL, cfg.ProjectURL),
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 50 * time.Minute, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("database listening", "port", cfg.HTTPPort)
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
