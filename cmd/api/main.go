package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Chiiz0/multi-codex/internal/api"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv()
	runtimeStore, err := store.Open(context.Background(), cfg.DatabaseURL, log)
	if err != nil {
		log.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer runtimeStore.Close()
	server := &http.Server{
		Addr:              cfg.APIListen,
		Handler:           api.NewServer(cfg, runtimeStore.Store, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("api server listening", "addr", cfg.APIListen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("api server failed", "error", err)
			os.Exit(1)
		}
	}()

	waitForShutdown(log, server)
}

func waitForShutdown(log *slog.Logger, server *http.Server) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	log.Info("api server stopped")
}
