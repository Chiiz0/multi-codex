package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestWriteAuditSealCreatesVerifiableBundle(t *testing.T) {
	output := filepath.Join(t.TempDir(), "seal")
	entries := []domain.AuditLog{
		{
			ID:           "audit_1",
			ActorType:    "human",
			ActorID:      "local-dev",
			Action:       "api.task_create",
			ResourceType: "task",
			ResourceID:   "task_1",
			Payload:      map[string]any{"status": "created"},
		},
	}
	verification := store.AuditHashVerification{
		Valid:     true,
		Total:     1,
		Hashed:    1,
		FirstHash: "first",
		LastHash:  "last",
	}

	manifest, err := writeAuditSeal(output, entries, verification)
	if err != nil {
		t.Fatalf("write audit seal: %v", err)
	}
	for _, name := range []string{"audit.jsonl", "manifest.json", "manifest.sha256"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("expected %s: %v", name, err)
		}
	}
	if manifest["audit_sha256"] == "" || manifest["manifest_sha256"] == "" {
		t.Fatalf("manifest missing hashes: %#v", manifest)
	}
	if manifest["bundle_format"] != "multi-codex.audit-seal.v1" {
		t.Fatalf("unexpected bundle format: %#v", manifest["bundle_format"])
	}

	auditBytes, err := os.ReadFile(filepath.Join(output, "audit.jsonl"))
	if err != nil {
		t.Fatalf("read audit jsonl: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(auditBytes)), "\n")
	if len(lines) != 1 {
		t.Fatalf("audit lines = %d, want 1", len(lines))
	}
	var exported domain.AuditLog
	if err := json.Unmarshal([]byte(lines[0]), &exported); err != nil {
		t.Fatalf("decode audit line: %v", err)
	}
	if exported.ID != entries[0].ID || exported.Action != entries[0].Action {
		t.Fatalf("unexpected exported entry: %#v", exported)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifestFile map[string]any
	if err := json.Unmarshal(manifestBytes, &manifestFile); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifestFile["entry_count"].(float64) != 1 {
		t.Fatalf("entry_count = %#v", manifestFile["entry_count"])
	}
}

func TestWriteAuditSealRejectsNonEmptyOutput(t *testing.T) {
	output := t.TempDir()
	if err := os.WriteFile(filepath.Join(output, "existing"), []byte("sealed"), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}
	_, err := writeAuditSeal(output, nil, store.AuditHashVerification{Valid: true})
	if err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Fatalf("expected non-empty output error, got %v", err)
	}
}
