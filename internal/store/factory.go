package store

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type RuntimeStore struct {
	Store Store
	Close func() error
}

func Open(ctx context.Context, databaseURL string, log *slog.Logger) (RuntimeStore, error) {
	if databaseURL == "" {
		log.Info("using in-memory store")
		return RuntimeStore{Store: NewMemoryStore(), Close: func() error { return nil }}, nil
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
	if err := pg.EnsureSeed(ctx); err != nil {
		_ = db.Close()
		return RuntimeStore{}, err
	}

	log.Info("using postgres store")
	return RuntimeStore{Store: pg, Close: db.Close}, nil
}
