package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/gateway/internal/gateway"
	"github.com/jp-cloud/gateway/internal/proxy"
	"github.com/jp-cloud/gateway/internal/static"
	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/otelx"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8000")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx := context.Background()

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	gwCfg := gateway.Config{
		Env: cfg.Env,
		Upstreams: map[string]string{
			"identity": cfg.IdentityURL, "organization": cfg.OrganizationURL, "project": cfg.ProjectURL,
			"repository": cfg.RepositoryURL, "deployment": cfg.DeploymentURL, "build": cfg.BuildURL,
			"registry": cfg.RegistryURL, "runtime": cfg.RuntimeURL, "domain": cfg.DomainURL,
			"certificate": cfg.CertificateURL, "secret": cfg.SecretURL, "logging": cfg.LoggingURL,
			"metrics": cfg.MetricsURL, "storage": cfg.StorageURL, "database": cfg.DatabaseURLSvc,
			"addon": cfg.AddonURL, "scheduler": cfg.SchedulerURL, "ai": cfg.AIURL,
		},
		CORSOrigins:        cfg.CORSOrigins,
		TrustedProxies:     cfg.TrustedProxies,
		PublicBaseURL:      cfg.PublicBaseURL,
		PublicDashboardURL: cfg.PublicDashboardURL,
		RegistrationMode:   cfg.RegistrationMode,
		JWTSecret:          cfg.JWTSecret,
	}

	// Dashboard: static files (lite) or reverse proxy to the dashboard container (full).
	var dash http.Handler
	if dir := os.Getenv("STATIC_DIR"); dir != "" {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			log.Info("serving static dashboard", "dir", dir)
			dash = static.NewHandler(dir)
		}
	} else if u := os.Getenv("DASHBOARD_URL"); u != "" {
		p, err := proxy.New(u)
		if err != nil {
			log.Error("dashboard url", "err", err)
			os.Exit(1)
		}
		log.Info("proxying dashboard to upstream", "url", u)
		dash = p
	}

	srv, err := gateway.New(gwCfg, gateway.Deps{
		JWT:     jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Redis:   rdb,
		Members: membership.New(cfg.OrganizationURL, ""),
		HTTP:    internalauth.NewClient(8 * time.Second),
		Log:     log,
	}, dash)
	if err != nil {
		log.Error("gateway", "err", err)
		os.Exit(1)
	}

	otelCfg := otelx.FromEnv("gateway")
	httpSrv := &http.Server{
		Addr:              ":" + cfg.HTTPPort,
		Handler:           otelx.Propagate(otelCfg)(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// Read/Write deadlines are applied per request by the router (streams and uploads differ).
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 64 << 10,
	}
	go func() {
		log.Info("gateway listening", "port", cfg.HTTPPort, "trusted_proxies", cfg.TrustedProxies)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
}
