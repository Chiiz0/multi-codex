package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/mcp"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.FromEnv()
	if err := config.ValidateProduction(cfg, "mcp-gateway"); err != nil {
		log.Error("production configuration rejected", "error", err)
		os.Exit(1)
	}
	runtimeStore, err := store.OpenWithConfig(context.Background(), cfg, log)
	if err != nil {
		log.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer runtimeStore.Close()
	server := &http.Server{
		Addr:              cfg.MCPListen,
		Handler:           mcp.NewServer(cfg, runtimeStore.Store, log).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("mcp gateway listening", "addr", cfg.MCPListen)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("mcp gateway failed", "error", err)
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
	log.Info("mcp gateway stopped")
}
