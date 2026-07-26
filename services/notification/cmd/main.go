package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/notification/internal/handlers"
)

func main() {
	cfg, err := config.Load("8004")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)

	h := &handlers.Handler{Log: log}
	mux := http.NewServeMux()
	h.Routes(mux)

	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	go func() {
		log.Info("notification listening", "port", cfg.HTTPPort)
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
