package store

import (
	"encoding/json"
	"os"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

const auditExportPathEnv = "MULTICODEX_AUDIT_EXPORT_PATH"

func exportAuditEntry(entry domain.AuditLog) error {
	path := os.Getenv(auditExportPathEnv)
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	return encoder.Encode(entry)
}
