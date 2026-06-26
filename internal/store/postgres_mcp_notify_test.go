package store

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestPostgresMCPSessionEventNotification(t *testing.T) {
	databaseURL := os.Getenv("MULTICODEX_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set MULTICODEX_TEST_DATABASE_URL to run PostgreSQL LISTEN/NOTIFY integration coverage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	st := NewPostgresStore(db, slog.New(slog.NewTextHandler(io.Discard, nil)), databaseURL)
	notifications, cleanup, err := st.SubscribeMCPSessionEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	sessionID := "session.notify.integration." + domain.NewID("mcp")
	defer db.ExecContext(context.Background(), `DELETE FROM mcp_sessions WHERE id = $1`, sessionID)
	now := time.Now().UTC()
	if _, err := st.UpsertMCPSession(domain.MCPSession{
		ID:              sessionID,
		ActorID:         "test",
		ProtocolVersion: "2025-06-18",
		Status:          "active",
		CreatedAt:       now,
		LastSeenAt:      now,
		ExpiresAt:       now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	event, err := st.AppendMCPSessionEvent(sessionID, "external", map[string]any{"type": "external"})
	if err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case notification := <-notifications:
			if notification.SessionID != sessionID {
				continue
			}
			if notification.Seq != event.Seq || notification.EventType != "external" {
				t.Fatalf("notification = %#v, event = %#v", notification, event)
			}
			return
		case <-ctx.Done():
			t.Fatalf("timed out waiting for MCP session event notification: %v", ctx.Err())
		}
	}
}
