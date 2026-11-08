package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/audit"
	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	"github.com/jp-cloud/go-common/ratelimit"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/go-common/revocation"
	"github.com/jp-cloud/go-common/secretbox"
	"github.com/jp-cloud/identity/internal/handlers"
	"github.com/jp-cloud/identity/internal/store"
)

func main() {
	cfg, err := config.Load("8001")
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
		migDir = filepath.Join("migrations")
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

	// Encryption material for TOTP seeds, backup-code hashes and secret platform settings.
	master := os.Getenv("IDENTITY_ENC_KEY")
	if master == "" {
		master = os.Getenv("SECRETS_MASTER_KEY")
	}
	if master == "" {
		master = cfg.JWTSecret
		log.Warn("IDENTITY_ENC_KEY (or SECRETS_MASTER_KEY) is not set: deriving the MFA/settings key from JWT_SECRET; rotating JWT_SECRET would invalidate stored TOTP seeds")
	}

	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	revoker := &revocation.Store{Redis: rdb}
	st := store.New(pool)
	h := &handlers.Handler{
		Store:            st,
		JWT:              jm,
		Redis:            rdb,
		Audit:            audit.NewWriter(pool),
		Log:              log,
		HTTP:             internalauth.NewClient(5 * time.Second),
		OrgURL:           cfg.OrganizationURL,
		Revoker:          revoker,
		Limiter:          &ratelimit.Limiter{Redis: rdb, Prefix: "jp:rl:id:"},
		RegistrationMode: cfg.RegistrationMode,
		BootstrapToken:   os.Getenv("BOOTSTRAP_TOKEN"),
		EncKey:           secretbox.DeriveKey(master, "identity-enc"),
		BackupKey:        secretbox.DeriveKey(master, "identity-backup"),
		Env:              cfg.Env,
	}

	// Re-encrypt / hash MFA material written by migration 002 (plaintext seeds and codes).
	upCtx, upCancel := context.WithTimeout(ctx, 60*time.Second)
	if n, err := st.UpgradeLegacyMFA(upCtx, h.SealForUpgrade, h.HashForUpgrade); err != nil {
		log.Error("mfa upgrade", "err", err)
		upCancel()
		os.Exit(1)
	} else if n > 0 {
		log.Info("upgraded legacy plaintext MFA rows", "count", n)
	}
	upCancel()

	// Revoked access tokens (logout / session revoke / password change) are rejected.
	// A Redis outage fails CLOSED.
	middleware.DenyCheckClaims = func(c context.Context, cl *jwtutil.Claims) bool {
		cc, cancel := context.WithTimeout(c, 2*time.Second)
		defer cancel()
		denied, err := revoker.Denied(cc, cl)
		if err != nil {
			log.Error("revocation check failed", "err", err)
			return true
		}
		return denied
	}

	mux := http.NewServeMux()
	h.Routes(mux)

	handler := middleware.Chain(mux,
		middleware.RequestID,
		middleware.Logging(log),
		middleware.CORS(cfg.CORSOrigins),
	)

	srv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}

	go func() {
		log.Info("identity listening", "port", cfg.HTTPPort, "registration", cfg.RegistrationMode)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
