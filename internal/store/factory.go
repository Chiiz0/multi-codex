package store

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type RuntimeStore struct {
	Store Store
	Close func() error
}

func Open(ctx context.Context, databaseURL string, log *slog.Logger) (RuntimeStore, error) {
	return OpenWithConfig(ctx, config.Config{DatabaseURL: databaseURL}, log)
}

func OpenWithConfig(ctx context.Context, cfg config.Config, log *slog.Logger) (RuntimeStore, error) {
	databaseURL := cfg.DatabaseURL
	if databaseURL == "" {
		log.Info("using in-memory store")
		return RuntimeStore{Store: NewMemoryStoreWithSeed(cfg.LocalAdminEmail, cfg.LocalAdminPassword), Close: func() error { return nil }}, nil
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return RuntimeStore{}, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return RuntimeStore{}, err
	}

	pg := NewPostgresStore(db, log, databaseURL)
	if err := pg.EnsureSeedWithCredentials(ctx, cfg.LocalAdminEmail, cfg.LocalAdminPassword); err != nil {
		_ = db.Close()
		return RuntimeStore{}, err
	}

	log.Info("using postgres store")
	return RuntimeStore{Store: pg, Close: db.Close}, nil
}
