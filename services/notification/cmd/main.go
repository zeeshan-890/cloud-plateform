package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/go-common/config"
	"github.com/jp-cloud/go-common/internalauth"
	"github.com/jp-cloud/go-common/logging"
	"github.com/jp-cloud/go-common/middleware"
	"github.com/jp-cloud/notification/internal/handlers"
	"github.com/jp-cloud/notification/internal/mailer"
)

func main() {
	cfg, err := config.Load("8004")
	if err != nil {
		panic(err)
	}
	log := logging.New(cfg.LogLevel)

	runCtx, cancelRun := context.WithCancel(context.Background())
	h := &handlers.Handler{
		Log:          log,
		Sender:       mailer.SMTP{},
		DashboardURL: cfg.PublicDashboardURL,
		Settings:     handlers.IdentitySettings(cfg.IdentityURL, internalauth.NewClient(5*time.Second)),
	}
	h.Start(runCtx, 2)
	if h.DashboardURL == "" {
		log.Warn("PUBLIC_DASHBOARD_URL is not set: invitation e-mails cannot be built")
	}
	mux := http.NewServeMux()
	h.Routes(mux)

	handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))
	srv := &http.Server{
		Addr: ":" + cfg.HTTPPort, Handler: handler,
		ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second, MaxHeaderBytes: 32 << 10,
	}
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
	cancelRun()
	h.Wait()
}
