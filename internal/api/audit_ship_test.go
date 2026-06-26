package api

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/auditseal"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestAuditShipWritesSealedBundleAndAuditsDecision(t *testing.T) {
	st := store.NewMemoryStore()
	st.RecordAuditLog(domain.AuditLog{ActorType: "human", ActorID: "user-1", Action: "api.task_create", ResourceType: "task", ResourceID: "task-1", Payload: map[string]any{"status": "created"}})
	st.RecordAuditLog(domain.AuditLog{ActorType: "worker", ActorID: "run-1", Action: "api.worker_result", ResourceType: "run", ResourceID: "run-1", Payload: map[string]any{"status": "succeeded"}})

	root := t.TempDir()
	sealRoot := filepath.Join(root, "seals")
	targetRoot := filepath.Join(root, "worm")
	server := NewServer(config.Config{AuditSealRoot: sealRoot, AuditShipTarget: "file://" + targetRoot}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	server.runAuditShip("test")

	seals := mustReadOneChild(t, sealRoot)
	if _, _, err := auditseal.VerifyBundle(filepath.Join(sealRoot, seals[0].Name())); err != nil {
		t.Fatalf("verify scheduled audit seal: %v", err)
	}
	shipped := mustReadOneChild(t, targetRoot)
	if _, err := os.Stat(filepath.Join(targetRoot, shipped[0].Name(), "receipt.json")); err != nil {
		t.Fatalf("receipt not written: %v", err)
	}
	payload, ok := findAuditPayload(st, "api.audit_ship")
	if !ok {
		t.Fatalf("api.audit_ship audit log not found")
	}
	if payload["trigger"] != "test" {
		t.Fatalf("trigger payload = %#v", payload["trigger"])
	}
	manifest, ok := payload["manifest"].(map[string]any)
	if !ok || manifest["entry_count"] != 2 {
		t.Fatalf("manifest payload = %#v", payload["manifest"])
	}
	receipt, ok := payload["receipt"].(map[string]any)
	if !ok || receipt["status"] != "shipped" {
		t.Fatalf("receipt payload = %#v", payload["receipt"])
	}
}

func TestAuditShipMissingTargetAuditsFailureWithoutBundle(t *testing.T) {
	st := store.NewMemoryStore()
	st.RecordAuditLog(domain.AuditLog{ActorType: "human", ActorID: "user-1", Action: "api.task_create", ResourceType: "task", ResourceID: "task-1"})

	sealRoot := filepath.Join(t.TempDir(), "seals")
	server := NewServer(config.Config{AuditSealRoot: sealRoot}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	server.runAuditShip("test")

	if _, err := os.Stat(sealRoot); !os.IsNotExist(err) {
		t.Fatalf("seal root should not be created, stat error = %v", err)
	}
	payload, ok := findAuditPayload(st, "api.audit_ship_failed")
	if !ok {
		t.Fatalf("api.audit_ship_failed audit log not found")
	}
	if payload["error"] == "" {
		t.Fatalf("failure payload missing error: %#v", payload)
	}
}

func mustReadOneChild(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one child under %s, got %d: %#v", root, len(entries), entries)
	}
	return entries
}

func findAuditPayload(st *store.MemoryStore, action string) (map[string]any, bool) {
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == action {
			return entry.Payload, true
		}
	}
	return nil, false
}
