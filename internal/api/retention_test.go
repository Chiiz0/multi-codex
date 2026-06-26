package api

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestRetentionCleanupAuditsMCPSessionResult(t *testing.T) {
	st := store.NewMemoryStore()
	now := time.Now().UTC()
	if _, err := st.UpsertMCPSession(domain.MCPSession{
		ID:              "session.retention",
		ActorID:         "main",
		ProtocolVersion: "2025-06-18",
		Status:          "active",
		CreatedAt:       now.Add(-4 * time.Hour),
		LastSeenAt:      now.Add(-4 * time.Hour),
		ExpiresAt:       now.Add(-3 * time.Hour),
	}); err != nil {
		t.Fatalf("upsert session: %v", err)
	}
	if _, err := st.AppendMCPSessionEvent("session.retention", "ready", map[string]any{"type": "ready"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := st.RevokeAuthToken(domain.AuthTokenRevocation{
		TokenHash: "expired-token-hash",
		Subject:   "subject-retention",
		ExpiresAt: now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if _, err := st.CreateAuthSession("expired-session-hash", st.GetAuthContext(), "oidc", "subject-retention", now.Add(-time.Minute)); err != nil {
		t.Fatalf("create auth session: %v", err)
	}
	if _, err := st.CreateAuthLoginState(domain.AuthLoginState{
		StateHash:    "expired-state-hash",
		NonceHash:    "expired-nonce-hash",
		CodeVerifier: "expired-verifier",
		ExpiresAt:    now.Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create auth login state: %v", err)
	}
	server := NewServer(config.Config{RetentionEnabled: false, RetentionMaxAge: time.Hour, RetentionDryRun: true}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	server.runRetentionCleanup("test")

	for _, entry := range st.ListAuditLogs() {
		if entry.Action != "api.retention_cleanup" {
			continue
		}
		result, ok := entry.Payload["mcp_sessions"].(domain.MCPSessionRetentionResult)
		if !ok {
			t.Fatalf("mcp_sessions payload type = %T", entry.Payload["mcp_sessions"])
		}
		if !result.DryRun || result.DeletedSessions != 1 || result.DeletedEvents != 1 {
			t.Fatalf("mcp session result = %#v, want one dry-run session/event", result)
		}
		revocations, ok := entry.Payload["auth_token_revocations"].(domain.AuthTokenRevocationRetentionResult)
		if !ok {
			t.Fatalf("auth_token_revocations payload type = %T", entry.Payload["auth_token_revocations"])
		}
		if !revocations.DryRun || revocations.Deleted != 1 {
			t.Fatalf("auth token revocation result = %#v, want one dry-run delete", revocations)
		}
		sessions, ok := entry.Payload["auth_sessions"].(domain.AuthSessionRetentionResult)
		if !ok {
			t.Fatalf("auth_sessions payload type = %T", entry.Payload["auth_sessions"])
		}
		if !sessions.DryRun || sessions.Deleted != 1 {
			t.Fatalf("auth session result = %#v, want one dry-run delete", sessions)
		}
		loginStates, ok := entry.Payload["auth_login_states"].(domain.AuthLoginStateRetentionResult)
		if !ok {
			t.Fatalf("auth_login_states payload type = %T", entry.Payload["auth_login_states"])
		}
		if !loginStates.DryRun || loginStates.Deleted != 1 {
			t.Fatalf("auth login state result = %#v, want one dry-run delete", loginStates)
		}
		return
	}
	t.Fatalf("expected api.retention_cleanup audit")
}
