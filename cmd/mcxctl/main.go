package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/auditseal"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/db"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/retention"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "migrate":
		migrate(log, os.Args[2:])
	case "retention-cleanup":
		retentionCleanup(log, os.Args[2:])
	case "backup":
		backup(log, os.Args[2:])
	case "restore":
		restore(log, os.Args[2:])
	case "audit-verify":
		auditVerify(log, os.Args[2:])
	case "audit-seal":
		auditSeal(log, os.Args[2:])
	case "audit-ship":
		auditShip(log, os.Args[2:])
	case "pilot-verify":
		pilotVerify(log, os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func migrate(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	databaseURL := fs.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL")
	migrationsDir := fs.String("migrations", "internal/db/migrations", "directory containing .sql migrations")
	_ = fs.Parse(args)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := db.Migrate(ctx, *databaseURL, *migrationsDir); err != nil {
		log.Error("migration failed", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied", "migrations", *migrationsDir)
}

func retentionCleanup(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("retention-cleanup", flag.ExitOnError)
	maxAge := fs.Duration("max-age", 30*24*time.Hour, "delete run/artifact directories older than this duration")
	dryRun := fs.Bool("dry-run", true, "preview cleanup without deleting")
	_ = fs.Parse(args)

	result := retention.Cleanup(retention.CleanupOptions{
		Roots:  []string{cfg.RunRoot, cfg.ArtifactRoot, cfg.WorktreeRoot},
		MaxAge: *maxAge,
		DryRun: *dryRun,
	})
	cutoff := time.Now().UTC().Add(-*maxAge)
	quietLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	runtimeStore, err := store.Open(context.Background(), cfg.DatabaseURL, quietLog)
	var mcpResult domain.MCPSessionRetentionResult
	var revocationResult domain.AuthTokenRevocationRetentionResult
	var authSessionResult domain.AuthSessionRetentionResult
	var loginStateResult domain.AuthLoginStateRetentionResult
	if err != nil {
		result.Errors = append(result.Errors, "mcp session cleanup: "+err.Error())
		result.Errors = append(result.Errors, "auth token revocation cleanup: "+err.Error())
		result.Errors = append(result.Errors, "auth session cleanup: "+err.Error())
		result.Errors = append(result.Errors, "auth login state cleanup: "+err.Error())
	} else {
		defer runtimeStore.Close()
		mcpResult, err = runtimeStore.Store.CleanupMCPSessions(cutoff, *dryRun)
		if err != nil {
			result.Errors = append(result.Errors, "mcp session cleanup: "+err.Error())
		}
		revocationResult, err = runtimeStore.Store.CleanupAuthTokenRevocations(time.Now().UTC(), *dryRun)
		if err != nil {
			result.Errors = append(result.Errors, "auth token revocation cleanup: "+err.Error())
		}
		authSessionResult, err = runtimeStore.Store.CleanupAuthSessions(time.Now().UTC(), *dryRun)
		if err != nil {
			result.Errors = append(result.Errors, "auth session cleanup: "+err.Error())
		}
		loginStateResult, err = runtimeStore.Store.CleanupAuthLoginStates(time.Now().UTC(), *dryRun)
		if err != nil {
			result.Errors = append(result.Errors, "auth login state cleanup: "+err.Error())
		}
	}
	output := struct {
		retention.CleanupResult
		MCPSessions          domain.MCPSessionRetentionResult          `json:"mcp_sessions"`
		AuthTokenRevocations domain.AuthTokenRevocationRetentionResult `json:"auth_token_revocations"`
		AuthSessions         domain.AuthSessionRetentionResult         `json:"auth_sessions"`
		AuthLoginStates      domain.AuthLoginStateRetentionResult      `json:"auth_login_states"`
	}{CleanupResult: result, MCPSessions: mcpResult, AuthTokenRevocations: revocationResult, AuthSessions: authSessionResult, AuthLoginStates: loginStateResult}
	writeJSONStdout(output)
	if len(result.Errors) > 0 {
		log.Error("retention cleanup completed with errors", "errors", result.Errors)
		os.Exit(1)
	}
}

func backup(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	output := fs.String("output", filepath.Join(".data", "backups", time.Now().UTC().Format("20060102T150405Z")), "backup output directory")
	_ = fs.Parse(args)

	if err := os.MkdirAll(*output, 0o755); err != nil {
		log.Error("create backup directory failed", "error", err)
		os.Exit(1)
	}
	manifest := map[string]any{
		"created_at":       time.Now().UTC(),
		"database_dump":    false,
		"artifact_archive": false,
		"run_archive":      false,
		"worktree_archive": false,
	}
	if cfg.DatabaseURL != "" {
		if _, err := exec.LookPath("pg_dump"); err == nil {
			path := filepath.Join(*output, "postgres.sql")
			if err := exec.Command("pg_dump", "--no-owner", "--no-privileges", "--file", path, cfg.DatabaseURL).Run(); err == nil {
				manifest["database_dump"] = true
			} else {
				manifest["database_error"] = err.Error()
			}
		} else {
			manifest["database_error"] = "pg_dump not found"
		}
	}
	manifest["artifact_archive"] = archiveIfExists(cfg.ArtifactRoot, filepath.Join(*output, "artifacts.tar.gz")) == nil
	manifest["run_archive"] = archiveIfExists(cfg.RunRoot, filepath.Join(*output, "runs.tar.gz")) == nil
	manifest["worktree_archive"] = archiveIfExists(cfg.WorktreeRoot, filepath.Join(*output, "worktrees.tar.gz")) == nil
	if err := writeJSONFile(filepath.Join(*output, "manifest.json"), manifest); err != nil {
		log.Error("write backup manifest failed", "error", err)
		os.Exit(1)
	}
	writeJSONStdout(manifest)
}

func restore(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("restore", flag.ExitOnError)
	input := fs.String("input", "", "backup directory created by mcxctl backup")
	_ = fs.Parse(args)
	if *input == "" {
		log.Error("restore input is required")
		os.Exit(2)
	}
	result := map[string]any{"input": *input, "restored_at": time.Now().UTC(), "database_restore": false}
	sqlPath := filepath.Join(*input, "postgres.sql")
	if cfg.DatabaseURL != "" {
		if _, err := os.Stat(sqlPath); err == nil {
			if _, err := exec.LookPath("psql"); err == nil {
				if err := exec.Command("psql", cfg.DatabaseURL, "-f", sqlPath).Run(); err == nil {
					result["database_restore"] = true
				} else {
					result["database_error"] = err.Error()
				}
			} else {
				result["database_error"] = "psql not found"
			}
		}
	}
	restoreArchive(filepath.Join(*input, "artifacts.tar.gz"), cfg.ArtifactRoot, result, "artifact_restore")
	restoreArchive(filepath.Join(*input, "runs.tar.gz"), cfg.RunRoot, result, "run_restore")
	restoreArchive(filepath.Join(*input, "worktrees.tar.gz"), cfg.WorktreeRoot, result, "worktree_restore")
	writeJSONStdout(result)
}

func auditVerify(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("audit-verify", flag.ExitOnError)
	databaseURL := fs.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL")
	allowLegacyHashMismatch := fs.Bool("allow-legacy-hash-mismatch", false, "treat pre-stable-canonicalization hash mismatches as warnings while still checking chain links")
	_ = fs.Parse(args)
	if *databaseURL == "" {
		log.Error("database url is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	entries, err := loadAuditLogs(ctx, *databaseURL)
	if err != nil {
		log.Error("load audit logs failed", "error", err)
		os.Exit(1)
	}
	result := store.VerifyAuditHashChainWithOptions(entries, store.AuditHashVerificationOptions{AllowLegacyHashMismatch: *allowLegacyHashMismatch})
	writeJSONStdout(result)
	if !result.Valid {
		os.Exit(1)
	}
}

func auditSeal(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("audit-seal", flag.ExitOnError)
	databaseURL := fs.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL")
	output := fs.String("output", filepath.Join(".data", "audit-seals", time.Now().UTC().Format("20060102T150405Z")), "empty output directory for the sealed audit bundle")
	allowLegacyHashMismatch := fs.Bool("allow-legacy-hash-mismatch", false, "allow leading pre-stable-canonicalization hash warnings before sealing")
	_ = fs.Parse(args)
	if *databaseURL == "" {
		log.Error("database url is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries, err := loadAuditLogs(ctx, *databaseURL)
	if err != nil {
		log.Error("load audit logs failed", "error", err)
		os.Exit(1)
	}
	verification := store.VerifyAuditHashChainWithOptions(entries, store.AuditHashVerificationOptions{AllowLegacyHashMismatch: *allowLegacyHashMismatch})
	if !verification.Valid {
		writeJSONStdout(verification)
		os.Exit(1)
	}
	manifest, err := auditseal.Write(*output, entries, verification)
	if err != nil {
		log.Error("write audit seal failed", "error", err)
		os.Exit(1)
	}
	writeJSONStdout(manifest)
}

func auditShip(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("audit-ship", flag.ExitOnError)
	input := fs.String("input", "", "audit seal bundle directory created by mcxctl audit-seal")
	target := fs.String("target", cfg.AuditShipTarget, "WORM/SIEM ingress directory or file:// directory")
	_ = fs.Parse(args)
	if *input == "" {
		log.Error("audit ship input is required")
		os.Exit(2)
	}
	if *target == "" {
		log.Error("audit ship target is required")
		os.Exit(2)
	}
	receipt, err := auditseal.Ship(*input, *target)
	if err != nil {
		log.Error("audit ship failed", "error", err)
		os.Exit(1)
	}
	writeJSONStdout(receipt)
}

func loadAuditLogs(ctx context.Context, databaseURL string) ([]domain.AuditLog, error) {
	dbConn, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	defer dbConn.Close()
	rows, err := dbConn.QueryContext(ctx, `
SELECT id::text, actor_type, actor_id, action, resource_type, resource_id, payload,
       COALESCE(prev_hash, ''), COALESCE(entry_hash, ''), created_at
FROM audit_logs
ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []domain.AuditLog{}
	for rows.Next() {
		var entry domain.AuditLog
		var payloadBytes []byte
		if err := rows.Scan(&entry.ID, &entry.ActorType, &entry.ActorID, &entry.Action, &entry.ResourceType, &entry.ResourceID, &payloadBytes, &entry.PrevHash, &entry.EntryHash, &entry.CreatedAt); err != nil {
			return nil, err
		}
		if len(payloadBytes) > 0 {
			if err := json.Unmarshal(payloadBytes, &entry.Payload); err != nil {
				return nil, fmt.Errorf("decode audit payload %s: %w", entry.ID, err)
			}
		}
		if entry.Payload == nil {
			entry.Payload = map[string]any{}
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func writeAuditSeal(output string, entries []domain.AuditLog, verification store.AuditHashVerification) (map[string]any, error) {
	return auditseal.Write(output, entries, verification)
}

func shipAuditSeal(input string, target string) (map[string]any, error) {
	return auditseal.Ship(input, target)
}

func verifyAuditSealBundle(input string) (map[string]any, string, error) {
	return auditseal.VerifyBundle(input)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  mcxctl migrate [-database-url URL] [-migrations DIR]")
	fmt.Fprintln(os.Stderr, "  mcxctl retention-cleanup [-max-age 720h] [-dry-run=true]")
	fmt.Fprintln(os.Stderr, "  mcxctl backup [-output DIR]")
	fmt.Fprintln(os.Stderr, "  mcxctl restore -input DIR")
	fmt.Fprintln(os.Stderr, "  mcxctl audit-verify [-database-url URL] [-allow-legacy-hash-mismatch=false]")
	fmt.Fprintln(os.Stderr, "  mcxctl audit-seal [-database-url URL] [-output DIR] [-allow-legacy-hash-mismatch=false]")
	fmt.Fprintln(os.Stderr, "  mcxctl audit-ship -input DIR [-target DIR|file://DIR]")
	fmt.Fprintln(os.Stderr, "  mcxctl pilot-verify -task-id ID [-strict=true] [-audit-ship-receipt PATH] [-backup-manifest PATH] [-restore-evidence PATH] [-signoff PATH]")
}

func archiveIfExists(root string, output string) error {
	if _, err := os.Stat(root); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		name, err := filepath.Rel(filepath.Dir(root), path)
		if err != nil {
			return err
		}
		header.Name = name
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		source, err := os.Open(path)
		if err != nil {
			return err
		}
		defer source.Close()
		_, err = io.Copy(tw, source)
		return err
	})
}

func restoreArchive(input string, target string, result map[string]any, key string) {
	if _, err := os.Stat(input); err != nil {
		result[key] = false
		return
	}
	file, err := os.Open(input)
	if err != nil {
		result[key+"_error"] = err.Error()
		return
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		result[key+"_error"] = err.Error()
		return
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	base := filepath.Clean(filepath.Dir(target))
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			result[key+"_error"] = err.Error()
			return
		}
		path := filepath.Clean(filepath.Join(base, header.Name))
		if path != base && !strings.HasPrefix(path, base+string(os.PathSeparator)) {
			result[key+"_error"] = "archive contains path outside restore target"
			return
		}
		if header.FileInfo().IsDir() {
			_ = os.MkdirAll(path, 0o755)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			result[key+"_error"] = err.Error()
			return
		}
		out, err := os.Create(path)
		if err != nil {
			result[key+"_error"] = err.Error()
			return
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			result[key+"_error"] = err.Error()
			return
		}
		_ = out.Close()
	}
	result[key] = true
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func writeJSONStdout(value any) {
	data, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(data))
}
