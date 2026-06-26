package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestAuditExportWritesJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(auditExportPathEnv, path)
	st := NewMemoryStore()

	st.RecordAuditLog(domain.AuditLog{ActorType: "human", ActorID: "user-1", Action: "api.task_create", ResourceType: "task", ResourceID: "task-1"})
	st.RecordAuditLog(domain.AuditLog{ActorType: "worker", ActorID: "run-1", Action: "api.worker_result", ResourceType: "run", ResourceID: "run-1"})

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open audit export: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
		var entry domain.AuditLog
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("decode exported entry: %v", err)
		}
		if entry.EntryHash == "" {
			t.Fatalf("exported entry has empty hash")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan export: %v", err)
	}
	if count != 2 {
		t.Fatalf("exported lines = %d, want 2", count)
	}
}
