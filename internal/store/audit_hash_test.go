package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestMemoryAuditLogHashChain(t *testing.T) {
	st := NewMemoryStore()

	first := st.RecordAuditLog(domain.AuditLog{
		ActorType:    "human",
		ActorID:      "local-dev",
		Action:       "api.task_created",
		ResourceType: "task",
		ResourceID:   "task_1",
		Payload:      map[string]any{"trace_id": "trace-1"},
	})
	second := st.RecordAuditLog(domain.AuditLog{
		ActorType:    "worker",
		ActorID:      "run_1",
		Action:       "api.worker_result",
		ResourceType: "run",
		ResourceID:   "run_1",
		Payload:      map[string]any{"status": "succeeded"},
	})

	if first.EntryHash == "" {
		t.Fatalf("first audit entry has empty hash")
	}
	if second.EntryHash == "" {
		t.Fatalf("second audit entry has empty hash")
	}
	if second.PrevHash != first.EntryHash {
		t.Fatalf("second prev hash = %q, want %q", second.PrevHash, first.EntryHash)
	}
	if first.EntryHash == second.EntryHash {
		t.Fatalf("audit entries should not share a hash")
	}
	result := VerifyAuditHashChain(st.ListAuditLogs())
	if !result.Valid || result.Hashed != 2 {
		t.Fatalf("verification result = %#v", result)
	}
	tamperedAfterStrict := second
	tamperedAfterStrict.Payload = map[string]any{"status": "tampered"}
	result = VerifyAuditHashChain([]domain.AuditLog{first, tamperedAfterStrict})
	if result.Valid || len(result.Errors) == 0 {
		t.Fatalf("expected tamper detection, got %#v", result)
	}
	result = VerifyAuditHashChainWithOptions([]domain.AuditLog{first, tamperedAfterStrict}, AuditHashVerificationOptions{AllowLegacyHashMismatch: true})
	if result.Valid || len(result.Errors) == 0 {
		t.Fatalf("expected post-strict tamper detection with legacy compatibility, got %#v", result)
	}
	legacyFirst := first
	legacyFirst.EntryHash = "legacy_hash"
	second.PrevHash = legacyFirst.EntryHash
	result = VerifyAuditHashChainWithOptions([]domain.AuditLog{legacyFirst, second}, AuditHashVerificationOptions{AllowLegacyHashMismatch: true})
	if !result.Valid || len(result.Warnings) == 0 {
		t.Fatalf("expected legacy warning, got %#v", result)
	}
}

func TestAuditHashUsesDatabaseTimestampPrecision(t *testing.T) {
	entry := prepareAuditEntry(domain.AuditLog{
		ID:           "audit_microsecond_test",
		ActorType:    "human",
		ActorID:      "local-dev",
		Action:       "api.audit_verify",
		ResourceType: "audit_log",
		ResourceID:   "audit_microsecond_test",
		Payload:      map[string]any{"status": "ok"},
		CreatedAt:    time.Date(2026, 6, 26, 10, 11, 12, 123456789, time.UTC),
	}, "")

	if got, want := entry.CreatedAt.Nanosecond(), 123456000; got != want {
		t.Fatalf("created_at nanosecond = %d, want %d", got, want)
	}
	result := VerifyAuditHashChain([]domain.AuditLog{entry})
	if !result.Valid || result.Hashed != 1 {
		t.Fatalf("verification result = %#v", result)
	}
}
