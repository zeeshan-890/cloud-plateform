package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/ai/internal/handlers"
	"github.com/jp-cloud/ai/internal/llm"
	"github.com/jp-cloud/go-common/config"
	jwtutil "github.com/jp-cloud/go-common/jwt"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	redisx "github.com/jp-cloud/go-common/redis"
)

func main() {
	cfg, err := config.Load("8019")
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

	client := llm.NewFromEnv()
	jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
	h := &handlers.Handler{
		JWT:             jm,
		OrganizationURL: cfg.OrganizationURL,
		BuildURL:        cfg.BuildURL,
		LoggingURL:      cfg.LoggingURL,
		DeploymentURL:   cfg.DeploymentURL,
		LLM:             client,
		Redis:           rdb,
		HTTP:            &http.Client{Timeout: 45 * time.Second},
		Log:             log,
	}

	mux := http.NewServeMux()
	h.Routes(mux)
	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("ai listening", "port", cfg.HTTPPort, "mode", client.Mode())
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
