// Command node is the JP node agent.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jp-cloud/node/internal/agent"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "node")
	cfg, err := agent.LoadConfig(os.Getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.RegistryUpstream != "" {
		rl, err := net.Listen("tcp", cfg.RegistryListen)
		if err != nil {
			log.Error("registry forwarder listen failed", "addr", cfg.RegistryListen, "err", err)
			os.Exit(1)
		}
		log.Info("registry forwarder started", "listen", cfg.RegistryListen, "upstream", cfg.RegistryUpstream)
		go agent.ForwardRegistry(ctx, agent.LimitListener(rl, 256), cfg.RegistryUpstream, log)
	}

	l, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		log.Error("listen failed", "addr", cfg.Listen, "err", err)
		os.Exit(1)
	}
	srv := &http.Server{Handler: agent.NewHandler(cfg, log), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if err := srv.Shutdown(sctx); err != nil {
			_ = srv.Close()
		}
	}()
	log.Info("node agent started", "name", cfg.Name, "listen", cfg.Listen, "docker_socket", cfg.DockerSocket)
	if err := srv.Serve(agent.LimitListener(l, 1024)); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}
