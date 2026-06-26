package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func prepareAuditEntry(entry domain.AuditLog, prevHash string) domain.AuditLog {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.CreatedAt = entry.CreatedAt.UTC().Truncate(time.Microsecond)
	if entry.Payload == nil {
		entry.Payload = map[string]any{}
	}
	if entry.PrevHash == "" {
		entry.PrevHash = prevHash
	}
	entry.EntryHash = auditEntryHash(entry)
	return entry
}

func auditEntryHash(entry domain.AuditLog) string {
	canonical := struct {
		ActorType    string         `json:"actor_type"`
		ActorID      string         `json:"actor_id"`
		Action       string         `json:"action"`
		ResourceType string         `json:"resource_type"`
		ResourceID   string         `json:"resource_id"`
		Payload      map[string]any `json:"payload"`
		PrevHash     string         `json:"prev_hash"`
		CreatedAt    string         `json:"created_at"`
	}{
		ActorType:    entry.ActorType,
		ActorID:      entry.ActorID,
		Action:       entry.Action,
		ResourceType: entry.ResourceType,
		ResourceID:   entry.ResourceID,
		Payload:      entry.Payload,
		PrevHash:     entry.PrevHash,
		CreatedAt:    entry.CreatedAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
	}
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

type AuditHashVerification struct {
	Valid     bool     `json:"valid"`
	Total     int      `json:"total"`
	Legacy    int      `json:"legacy"`
	Hashed    int      `json:"hashed"`
	FirstHash string   `json:"first_hash,omitempty"`
	LastHash  string   `json:"last_hash,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Errors    []string `json:"errors,omitempty"`
}

type AuditHashVerificationOptions struct {
	AllowLegacyHashMismatch bool
}

func VerifyAuditHashChain(entries []domain.AuditLog) AuditHashVerification {
	return VerifyAuditHashChainWithOptions(entries, AuditHashVerificationOptions{})
}

func VerifyAuditHashChainWithOptions(entries []domain.AuditLog, options AuditHashVerificationOptions) AuditHashVerification {
	result := AuditHashVerification{Valid: true, Total: len(entries)}
	ordered := append([]domain.AuditLog(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})
	var expectedPrev string
	chainStarted := false
	strictSegmentStarted := false
	for _, entry := range ordered {
		if entry.EntryHash == "" {
			if chainStarted {
				result.Valid = false
				result.Errors = append(result.Errors, fmt.Sprintf("audit log %s is missing entry_hash after chain start", entry.ID))
			} else {
				result.Legacy++
			}
			continue
		}
		chainStarted = true
		result.Hashed++
		if result.FirstHash == "" {
			result.FirstHash = entry.EntryHash
		}
		if entry.PrevHash != expectedPrev {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("audit log %s prev_hash = %q, want %q", entry.ID, entry.PrevHash, expectedPrev))
		}
		if got := auditEntryHash(entry); got != entry.EntryHash {
			if options.AllowLegacyHashMismatch && !strictSegmentStarted {
				result.Legacy++
				result.Warnings = append(result.Warnings, fmt.Sprintf("audit log %s entry_hash uses a legacy canonicalization and could not be recomputed", entry.ID))
				expectedPrev = entry.EntryHash
				result.LastHash = entry.EntryHash
				continue
			}
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("audit log %s entry_hash mismatch", entry.ID))
		}
		strictSegmentStarted = true
		expectedPrev = entry.EntryHash
		result.LastHash = entry.EntryHash
	}
	return result
}
