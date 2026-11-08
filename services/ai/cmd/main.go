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

	"github.com/jp-cloud/ai/internal/crypt"
	"github.com/jp-cloud/ai/internal/handlers"
	"github.com/jp-cloud/ai/internal/llm"
	"github.com/jp-cloud/ai/internal/store"
	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/membership"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/go-common/postgres"
	redisx "github.com/jp-cloud/go-common/redis"
	"github.com/redis/go-redis/v9"
)

// redisLimiter allows N LLM calls per organisation per minute (fixed window).
type redisLimiter struct {
	rdb   *redis.Client
	limit int64
}

func (l *redisLimiter) Allow(ctx context.Context, orgID string) (bool, error) {
	key := fmt.Sprintf("jp:ai:rl:%s:%d", orgID, time.Now().Unix()/60)
	n, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if n == 1 {
		_ = l.rdb.Expire(ctx, key, 2*time.Minute).Err()
	}
	return n <= l.limit, nil
}

// platformConfig builds the optional platform-wide fallback from the environment.
func platformConfig() llm.Config {
	provider := strings.ToLower(os.Getenv("AI_PLATFORM_PROVIDER"))
	key := strings.TrimSpace(os.Getenv("AI_PLATFORM_KEY"))
	model := strings.TrimSpace(os.Getenv("AI_PLATFORM_MODEL"))
	if key == "" { // legacy variables
		for _, e := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"} {
			if v := strings.TrimSpace(strings.Split(os.Getenv(e), ",")[0]); v != "" {
				key, provider = v, llm.ProviderGemini
				break
			}
		}
		if key == "" {
			if v := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); v != "" {
				key, provider = v, llm.ProviderOpenAI
			}
		}
		if model == "" {
			model = firstEnv("AI_MODEL", "GEMINI_MODEL")
		}
	}
	if key == "" {
		return llm.Config{}
	}
	if !llm.ValidProvider(provider) {
		provider = llm.ProviderGemini
	}
	if model == "" || !llm.ValidModel(model) {
		model = llm.DefaultModel(provider)
	}
	c := llm.Config{Provider: provider, Model: model, APIKey: key}
	if provider == llm.ProviderOpenAI {
		c.BaseURL = strings.TrimRight(os.Getenv("AI_BASE_URL"), "/")
	}
	return c
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

func main() {
	cfg, err := config.Load("8019")
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
	// The old implementation kept ONE global key/model in Redis that any member could overwrite.
	if n, _ := rdb.Del(ctx, "jp:ai:config").Result(); n > 0 {
		log.Warn("removed legacy global AI configuration (jp:ai:config); configure AI per organisation")
	}

	box, err := crypt.FromEnv()
	if err != nil {
		log.Warn("AI key encryption not configured: organisation API keys cannot be stored", "err", err)
		box = nil
	}
	limit := int64(20)
	if v := os.Getenv("AI_RATE_PER_MIN"); v != "" {
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil && n > 0 {
			limit = n
		}
	}
	allowPrivate := strings.EqualFold(os.Getenv("AI_ALLOW_PRIVATE_BASE_URL"), "true")
	platform := platformConfig()
	h := &handlers.Handler{
		Store: store.New(pool), Box: box, LLM: llm.New(), Limit: &redisLimiter{rdb: rdb, limit: limit},
		Authz: membership.New(cfg.OrganizationURL, cfg.ProjectURL), AllowPrivate: allowPrivate, Platform: platform, Log: log,
		JWT: jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL),
		Tools: handlers.Tools{HTTP: internalauth.NewClient(15 * time.Second), BuildURL: cfg.BuildURL, DeploymentURL: cfg.DeploymentURL,
			RuntimeURL: cfg.RuntimeURL, LoggingURL: cfg.LoggingURL},
	}
	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: 120 * time.Second}
	go func() {
		log.Info("ai listening", "port", cfg.HTTPPort, "platform_fallback", platform.APIKey != "", "org_keys", box != nil, "rate_per_min", limit)
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
