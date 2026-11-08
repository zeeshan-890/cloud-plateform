package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/httpx"
	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/jp-cloud/worker/internal/builder"
	"github.com/jp-cloud/worker/internal/redact"
	"github.com/jp-cloud/worker/internal/scan"
	"github.com/jp-cloud/worker/internal/worker"
)

func main() {
	cfg, err := config.Load("8008")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// BUILD_MODE: "docker" (default, real builds) or an explicit "simulate" (dev only).
	mode := strings.ToLower(getEnv("BUILD_MODE", "docker"))
	switch mode {
	case "docker", "buildkit", "real":
		mode = "docker"
	case "simulate":
		if strings.EqualFold(cfg.Env, "production") {
			log.Error("BUILD_MODE=simulate is not allowed when JP_ENV=production")
			os.Exit(1)
		}
	default:
		log.Error("invalid BUILD_MODE", "mode", mode)
		os.Exit(1)
	}

	rdb, err := redisx.Connect(ctx, cfg.RedisURL)
	if err != nil {
		log.Error("redis", "err", err)
		os.Exit(1)
	}
	defer rdb.Close()

	hostname, _ := os.Hostname()
	registryHost := strings.TrimRight(getEnv("REGISTRY_PUSH_URL", os.Getenv("REGISTRY_HOST")), "/")
	registryHost = strings.TrimPrefix(strings.TrimPrefix(registryHost, "http://"), "https://")
	if registryHost == "" {
		log.Error("REGISTRY_HOST (or REGISTRY_PUSH_URL) is required: the address the HOST docker daemon uses to push and pull images")
		os.Exit(1)
	}

	dk := builder.DefaultDocker()
	trivyOn := config.GetEnvBool("TRIVY_ENABLED", true)
	tcache := os.Getenv("TRIVY_CACHE_DIR")
	red := redact.New(os.Getenv("GITHUB_TOKEN"), os.Getenv("REGISTRY_PASSWORD"))
	w := &worker.Worker{
		BuildURL:        strings.TrimRight(cfg.BuildURL, "/"),
		DeploymentURL:   strings.TrimRight(cfg.DeploymentURL, "/"),
		RepositoryURL:   strings.TrimRight(cfg.RepositoryURL, "/"),
		RegistryAPI:     strings.TrimRight(cfg.RegistryURL, "/"),
		RegistryHost:    registryHost,
		RegistryCheck:   os.Getenv("REGISTRY_CHECK_URL"),
		Simulate:        mode == "simulate",
		HTTP:            internalauth.NewClient(30 * time.Second),
		Log:             log,
		Redactor:        red,
		Docker:          dk,
		WorkRoot:        getEnv("WORK_DIR", os.TempDir()),
		MinFreeBytes:    uint64(envInt("WORKER_MIN_FREE_MB", 2048)) << 20,
		MaxRepoBytes:    int64(envInt("WORKER_MAX_REPO_MB", 512)) << 20,
		CloneTimeout:    envDur("WORKER_CLONE_TIMEOUT", 3*time.Minute),
		BuildTimeout:    envDur("WORKER_BUILD_TIMEOUT", 15*time.Minute),
		PushTimeout:     envDur("WORKER_PUSH_TIMEOUT", 5*time.Minute),
		AllowedGitHosts: splitCSV(os.Getenv("GIT_ALLOWED_HOSTS")),
	}
	w.Scan = func(c context.Context, ref string) *scan.Summary {
		return scan.Run(c, ref, scan.Options{Enabled: trivyOn, CacheDir: tcache, Timeout: envDur("TRIVY_TIMEOUT", 5*time.Minute)})
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", httpx.Healthz)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		log.Info("worker listening", "port", cfg.HTTPPort, "mode", mode, "consumer", consumerName(hostname), "trivy", trivyOn)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("server", "err", err)
		}
	}()

	if err := w.Run(ctx, worker.RunConfig{
		Redis: rdb, Consumer: consumerName(hostname), Concurrency: envInt("WORKER_CONCURRENCY", 1),
		ClaimIdle: envDur("WORKER_CLAIM_IDLE", 10*time.Minute), DrainTimeout: envDur("WORKER_DRAIN_TIMEOUT", 10*time.Minute),
		MaxAttempts: envInt("WORKER_MAX_ATTEMPTS", 3),
	}); err != nil {
		log.Error("worker stopped", "err", err)
	}
	sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
}

func consumerName(host string) string {
	if v := os.Getenv("WORKER_NAME"); v != "" {
		return v
	}
	if host != "" {
		return host
	}
	return "worker-1"
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil && n > 0 {
		return n
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil && d > 0 {
		return d
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
