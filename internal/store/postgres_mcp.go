package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/Chiiz0/multi-codex/internal/domain"
)

const mcpSessionEventsNotifyChannel = "multi_codex_mcp_session_events"

func (s *PostgresStore) UpsertMCPSession(session domain.MCPSession) (domain.MCPSession, error) {
	if session.ID == "" {
		return domain.MCPSession{}, ErrInvalidID
	}
	now := time.Now().UTC()
	if session.ProtocolVersion == "" {
		session.ProtocolVersion = "2025-06-18"
	}
	if session.Status == "" {
		session.Status = "active"
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.LastSeenAt.IsZero() {
		session.LastSeenAt = now
	}
	if session.ExpiresAt.IsZero() {
		session.ExpiresAt = session.LastSeenAt
	}

	ctx, cancel := storeContext()
	defer cancel()
	err := s.db.QueryRowContext(ctx, `
INSERT INTO mcp_sessions (id, actor_id, protocol_version, status, created_at, last_seen_at, expires_at, last_event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE
SET actor_id = EXCLUDED.actor_id,
    protocol_version = EXCLUDED.protocol_version,
    status = EXCLUDED.status,
    last_seen_at = EXCLUDED.last_seen_at,
    expires_at = EXCLUDED.expires_at,
    last_event_id = GREATEST(mcp_sessions.last_event_id, EXCLUDED.last_event_id)
RETURNING id, actor_id, protocol_version, status, created_at, last_seen_at, expires_at, last_event_id`,
		session.ID, session.ActorID, session.ProtocolVersion, session.Status, session.CreatedAt,
		session.LastSeenAt, session.ExpiresAt, session.LastEventID,
	).Scan(&session.ID, &session.ActorID, &session.ProtocolVersion, &session.Status, &session.CreatedAt,
		&session.LastSeenAt, &session.ExpiresAt, &session.LastEventID)
	return session, err
}

func (s *PostgresStore) GetMCPSession(id string) (domain.MCPSession, error) {
	ctx, cancel := storeContext()
	defer cancel()

	var session domain.MCPSession
	err := s.db.QueryRowContext(ctx, `
SELECT id, actor_id, protocol_version, status, created_at, last_seen_at, expires_at, last_event_id
FROM mcp_sessions
WHERE id = $1`, id).Scan(&session.ID, &session.ActorID, &session.ProtocolVersion, &session.Status,
		&session.CreatedAt, &session.LastSeenAt, &session.ExpiresAt, &session.LastEventID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.MCPSession{}, ErrNotFound
	}
	return session, err
}

func (s *PostgresStore) AppendMCPSessionEvent(sessionID string, eventType string, payload map[string]any) (domain.MCPSessionEvent, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadBytes, _ := json.Marshal(payload)
	now := time.Now().UTC()
	ctx, cancel := storeContext()
	defer cancel()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.MCPSessionEvent{}, err
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRowContext(ctx, `
UPDATE mcp_sessions
SET last_event_id = last_event_id + 1,
    last_seen_at = $2
WHERE id = $1
RETURNING last_event_id`, sessionID, now).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.MCPSessionEvent{}, ErrNotFound
		}
		return domain.MCPSessionEvent{}, err
	}

	event := domain.MCPSessionEvent{SessionID: sessionID, Seq: seq, EventType: eventType, Payload: payload}
	if err := tx.QueryRowContext(ctx, `
INSERT INTO mcp_session_events (session_id, seq, event_type, payload, created_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id::text, session_id, seq, event_type, payload, created_at`,
		sessionID, seq, eventType, payloadBytes, now,
	).Scan(&event.ID, &event.SessionID, &event.Seq, &event.EventType, &payloadBytes, &event.CreatedAt); err != nil {
		return domain.MCPSessionEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.MCPSessionEvent{}, err
	}
	event.Payload = decodeMap(payloadBytes)
	s.notifyMCPSessionEvent(ctx, domain.MCPSessionEventNotification{
		SessionID: event.SessionID,
		Seq:       event.Seq,
		EventType: event.EventType,
	})
	return event, nil
}

func (s *PostgresStore) notifyMCPSessionEvent(ctx context.Context, notification domain.MCPSessionEventNotification) {
	payload, err := json.Marshal(notification)
	if err != nil {
		s.log.Warn("marshal MCP session notification failed", "error", err)
		return
	}
	if _, err := s.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, mcpSessionEventsNotifyChannel, string(payload)); err != nil {
		s.log.Warn("publish MCP session notification failed", "session_id", notification.SessionID, "seq", notification.Seq, "error", err)
	}
}

func (s *PostgresStore) SubscribeMCPSessionEvents(ctx context.Context) (<-chan domain.MCPSessionEventNotification, func(), error) {
	if s.databaseURL == "" {
		return nil, nil, errors.New("postgres database URL is required for MCP session event subscription")
	}
	listenCtx, cancel := context.WithCancel(ctx)
	conn, err := pgx.Connect(listenCtx, s.databaseURL)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if _, err := conn.Exec(listenCtx, `LISTEN `+mcpSessionEventsNotifyChannel); err != nil {
		cancel()
		_ = conn.Close(context.Background())
		return nil, nil, err
	}
	notifications := make(chan domain.MCPSessionEventNotification, 100)
	go func() {
		defer close(notifications)
		defer conn.Close(context.Background())
		for {
			msg, err := conn.WaitForNotification(listenCtx)
			if err != nil {
				if listenCtx.Err() == nil {
					s.log.Warn("MCP session event listener stopped", "error", err)
				}
				return
			}
			var notification domain.MCPSessionEventNotification
			if err := json.Unmarshal([]byte(msg.Payload), &notification); err != nil {
				s.log.Warn("decode MCP session notification failed", "error", err)
				continue
			}
			if notification.SessionID == "" || notification.Seq <= 0 {
				s.log.Warn("ignore incomplete MCP session notification", "payload", msg.Payload)
				continue
			}
			select {
			case notifications <- notification:
			case <-listenCtx.Done():
				return
			default:
				s.log.Warn("drop MCP session notification because subscriber buffer is full", "session_id", notification.SessionID, "seq", notification.Seq)
			}
		}
	}()
	return notifications, cancel, nil
}

func (s *PostgresStore) ListMCPSessionEventsAfter(sessionID string, afterSeq int64, limit int) []domain.MCPSessionEvent {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	ctx, cancel := storeContext()
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, session_id, seq, event_type, payload, created_at
FROM mcp_session_events
WHERE session_id = $1 AND seq > $2
ORDER BY seq ASC
LIMIT $3`, sessionID, afterSeq, limit)
	if err != nil {
		s.log.Error("list MCP session events failed", "error", err)
		return []domain.MCPSessionEvent{}
	}
	defer rows.Close()

	events := []domain.MCPSessionEvent{}
	for rows.Next() {
		var event domain.MCPSessionEvent
		var payloadBytes []byte
		if err := rows.Scan(&event.ID, &event.SessionID, &event.Seq, &event.EventType, &payloadBytes, &event.CreatedAt); err != nil {
			s.log.Error("scan MCP session event failed", "error", err)
			return events
		}
		event.Payload = decodeMap(payloadBytes)
		events = append(events, event)
	}
	return events
}

func (s *PostgresStore) CleanupMCPSessions(cutoff time.Time, dryRun bool) (domain.MCPSessionRetentionResult, error) {
	ctx, cancel := storeContext()
	defer cancel()

	result := domain.MCPSessionRetentionResult{DryRun: dryRun, Cutoff: cutoff}
	err := s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM mcp_sessions
WHERE expires_at < $1`, cutoff).Scan(&result.ScannedSessions)
	if err != nil {
		return result, err
	}
	err = s.db.QueryRowContext(ctx, `
SELECT count(*)
FROM mcp_session_events e
JOIN mcp_sessions s ON s.id = e.session_id
WHERE s.expires_at < $1`, cutoff).Scan(&result.DeletedEvents)
	if err != nil {
		return result, err
	}
	result.DeletedSessions = result.ScannedSessions
	if dryRun || result.ScannedSessions == 0 {
		return result, nil
	}
	cmd, err := s.db.ExecContext(ctx, `
DELETE FROM mcp_sessions
WHERE expires_at < $1`, cutoff)
	if err != nil {
		return result, err
	}
	if affected, err := cmd.RowsAffected(); err == nil {
		result.DeletedSessions = affected
	}
	return result, nil
}
