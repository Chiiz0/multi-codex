package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestMemoryCleanupMCPSessions(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	oldSession := domain.MCPSession{
		ID:              "session.old",
		ActorID:         "main",
		ProtocolVersion: "2025-06-18",
		Status:          "active",
		CreatedAt:       now.Add(-4 * time.Hour),
		LastSeenAt:      now.Add(-4 * time.Hour),
		ExpiresAt:       now.Add(-3 * time.Hour),
	}
	newSession := domain.MCPSession{
		ID:              "session.new",
		ActorID:         "main",
		ProtocolVersion: "2025-06-18",
		Status:          "active",
		CreatedAt:       now,
		LastSeenAt:      now,
		ExpiresAt:       now.Add(time.Hour),
	}
	if _, err := st.UpsertMCPSession(oldSession); err != nil {
		t.Fatalf("upsert old session: %v", err)
	}
	if _, err := st.AppendMCPSessionEvent(oldSession.ID, "ready", map[string]any{"type": "ready"}); err != nil {
		t.Fatalf("append old event: %v", err)
	}
	if _, err := st.UpsertMCPSession(newSession); err != nil {
		t.Fatalf("upsert new session: %v", err)
	}

	dryRun, err := st.CleanupMCPSessions(now.Add(-2*time.Hour), true)
	if err != nil {
		t.Fatalf("dry cleanup: %v", err)
	}
	if !dryRun.DryRun || dryRun.DeletedSessions != 1 || dryRun.DeletedEvents != 1 {
		t.Fatalf("dry cleanup = %#v, want one session/event", dryRun)
	}
	if _, err := st.GetMCPSession(oldSession.ID); err != nil {
		t.Fatalf("dry run deleted old session: %v", err)
	}

	deleted, err := st.CleanupMCPSessions(now.Add(-2*time.Hour), false)
	if err != nil {
		t.Fatalf("delete cleanup: %v", err)
	}
	if deleted.DryRun || deleted.DeletedSessions != 1 || deleted.DeletedEvents != 1 {
		t.Fatalf("delete cleanup = %#v, want one session/event", deleted)
	}
	if _, err := st.GetMCPSession(oldSession.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old session error = %v, want ErrNotFound", err)
	}
	if events := st.ListMCPSessionEventsAfter(oldSession.ID, 0, 10); len(events) != 0 {
		t.Fatalf("old session events length = %d, want 0", len(events))
	}
	if _, err := st.GetMCPSession(newSession.ID); err != nil {
		t.Fatalf("new session missing after cleanup: %v", err)
	}
}

func TestMemoryCleanupAuthTokenRevocations(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	if _, err := st.RevokeAuthToken(domain.AuthTokenRevocation{TokenHash: "old-token", ExpiresAt: now.Add(-time.Hour)}); err != nil {
		t.Fatalf("revoke old token: %v", err)
	}
	if _, err := st.RevokeAuthToken(domain.AuthTokenRevocation{TokenHash: "new-token", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatalf("revoke new token: %v", err)
	}

	dryRun, err := st.CleanupAuthTokenRevocations(now, true)
	if err != nil {
		t.Fatalf("dry cleanup: %v", err)
	}
	if !dryRun.DryRun || dryRun.Deleted != 1 {
		t.Fatalf("dry cleanup = %#v, want one revocation", dryRun)
	}

	deleted, err := st.CleanupAuthTokenRevocations(now, false)
	if err != nil {
		t.Fatalf("delete cleanup: %v", err)
	}
	if deleted.DryRun || deleted.Deleted != 1 {
		t.Fatalf("delete cleanup = %#v, want one revocation", deleted)
	}
	if st.IsAuthTokenRevoked("old-token", now.Add(-2*time.Hour)) {
		t.Fatalf("old token still present after cleanup")
	}
	if !st.IsAuthTokenRevoked("new-token", now) {
		t.Fatalf("new token missing after cleanup")
	}
}

func TestMemoryCleanupAuthSessions(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	auth := st.GetAuthContext()
	if _, err := st.CreateAuthSession("old-session", auth, "oidc", "old-subject", now.Add(-time.Hour)); err != nil {
		t.Fatalf("create old session: %v", err)
	}
	if _, err := st.CreateAuthSession("new-session", auth, "oidc", "new-subject", now.Add(time.Hour)); err != nil {
		t.Fatalf("create new session: %v", err)
	}

	dryRun, err := st.CleanupAuthSessions(now, true)
	if err != nil {
		t.Fatalf("dry cleanup: %v", err)
	}
	if !dryRun.DryRun || dryRun.Deleted != 1 {
		t.Fatalf("dry cleanup = %#v, want one session", dryRun)
	}

	deleted, err := st.CleanupAuthSessions(now, false)
	if err != nil {
		t.Fatalf("delete cleanup: %v", err)
	}
	if deleted.DryRun || deleted.Deleted != 1 {
		t.Fatalf("delete cleanup = %#v, want one session", deleted)
	}
	if _, _, err := st.GetAuthSession("old-session", now.Add(-2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old session error = %v, want ErrNotFound", err)
	}
	if _, _, err := st.GetAuthSession("new-session", now); err != nil {
		t.Fatalf("new session missing after cleanup: %v", err)
	}
}

func TestMemoryCleanupAuthLoginStates(t *testing.T) {
	st := NewMemoryStore()
	now := time.Now().UTC()
	if _, err := st.CreateAuthLoginState(domain.AuthLoginState{
		StateHash:    "old-state",
		NonceHash:    "old-nonce",
		CodeVerifier: "old-verifier",
		ExpiresAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatalf("create old login state: %v", err)
	}
	if _, err := st.CreateAuthLoginState(domain.AuthLoginState{
		StateHash:    "new-state",
		NonceHash:    "new-nonce",
		CodeVerifier: "new-verifier",
		ExpiresAt:    now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("create new login state: %v", err)
	}

	dryRun, err := st.CleanupAuthLoginStates(now, true)
	if err != nil {
		t.Fatalf("dry cleanup: %v", err)
	}
	if !dryRun.DryRun || dryRun.Deleted != 1 {
		t.Fatalf("dry cleanup = %#v, want one login state", dryRun)
	}

	deleted, err := st.CleanupAuthLoginStates(now, false)
	if err != nil {
		t.Fatalf("delete cleanup: %v", err)
	}
	if deleted.DryRun || deleted.Deleted != 1 {
		t.Fatalf("delete cleanup = %#v, want one login state", deleted)
	}
	if _, err := st.ConsumeAuthLoginState("old-state", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old login state error = %v, want ErrNotFound", err)
	}
	if _, err := st.ConsumeAuthLoginState("new-state", now); err != nil {
		t.Fatalf("new login state missing after cleanup: %v", err)
	}
}
