package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Migration struct {
	Version string
	Path    string
	SQL     string
	SHA256  string
}

func Migrate(ctx context.Context, databaseURL string, migrationsDir string) error {
	if databaseURL == "" {
		return fmt.Errorf("database URL is required")
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	if err := db.PingContext(ctx); err != nil {
		return err
	}
	if err := ensureMigrationTable(ctx, db); err != nil {
		return err
	}

	migrations, err := readMigrations(migrationsDir)
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		applied, err := migrationApplied(ctx, db, migration)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

func ensureMigrationTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version text PRIMARY KEY,
  path text NOT NULL,
  sha256 text NOT NULL,
  applied_at timestamptz NOT NULL DEFAULT now()
)`)
	return err
}

func readMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(content)
		version := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		migrations = append(migrations, Migration{
			Version: version,
			Path:    path,
			SQL:     string(content),
			SHA256:  hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	return migrations, nil
}

func migrationApplied(ctx context.Context, db *sql.DB, migration Migration) (bool, error) {
	var storedHash string
	err := db.QueryRowContext(ctx, `SELECT sha256 FROM schema_migrations WHERE version = $1`, migration.Version).Scan(&storedHash)
	if err == nil {
		if storedHash != migration.SHA256 {
			return false, fmt.Errorf("migration %s hash changed after apply", migration.Version)
		}
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, err
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("apply migration %s: %w", migration.Version, err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, path, sha256, applied_at)
VALUES ($1, $2, $3, $4)`, migration.Version, migration.Path, migration.SHA256, time.Now().UTC()); err != nil {
		return err
	}
	return tx.Commit()
}
