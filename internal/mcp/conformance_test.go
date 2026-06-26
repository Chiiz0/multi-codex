package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestStreamableHTTPPostRequiresJSONAndSSEAccept(t *testing.T) {
	server := NewServer(config.Config{}, store.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestStreamableHTTPNotificationReturnsAcceptedNoBody(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.test")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if strings.TrimSpace(resp.Body.String()) != "" {
		t.Fatalf("expected empty notification response body, got %q", resp.Body.String())
	}
	if !foundSessionAudit(st, "mcp.session_notification", "session.test") {
		t.Fatalf("expected notification session audit")
	}
}

func TestStreamableHTTPGetRequiresSSEAccept(t *testing.T) {
	server := NewServer(config.Config{}, store.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Accept", "application/json")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestStreamableHTTPGetStartsSSEWithAcceptedHeader(t *testing.T) {
	server := NewServer(config.Config{}, store.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	resp := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if contentType := resp.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(resp.Body.String(), "multi-codex-mcp-gateway") {
		t.Fatalf("missing ready event: %s", resp.Body.String())
	}
}

func TestStreamableHTTPExpiredSessionReturnsNotFoundAndAudits(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{MCPSessionTTL: time.Nanosecond}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.expiring")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", resp.Code, resp.Body.String())
	}

	time.Sleep(time.Millisecond)
	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.expiring")
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusNotFound {
		t.Fatalf("expired status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !foundSessionAudit(st, "mcp.session_expired", "session.expiring") {
		t.Fatalf("expected expired session audit")
	}
}

func TestStreamableHTTPStreamResumeAuditsLastEventID(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{MCPSessionTTL: time.Hour}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.resume")
	req.Header.Set("Last-Event-ID", "7")
	resp := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "id: 8") || !strings.Contains(resp.Body.String(), `"resumed":true`) {
		t.Fatalf("resume SSE body = %s", resp.Body.String())
	}
	if !foundSessionAuditPayload(st, "mcp.session_resume", "session.resume", func(payload map[string]any) bool {
		return payload["client_last_event_id"] == "7" && payload["last_event_id"] == "8" && payload["resumed"] == true
	}) {
		t.Fatalf("expected resume session audit")
	}
}

func TestStreamableHTTPStreamResumeReplaysPersistedEvents(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{MCPSessionTTL: time.Hour}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.replay")
	resp := &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "id: 1") {
		t.Fatalf("initial SSE body = %s", resp.Body.String())
	}

	ctx, cancel = context.WithCancel(context.Background())
	cancel()
	req = httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.replay")
	req.Header.Set("Last-Event-ID", "0")
	resp = &flushRecorder{ResponseRecorder: httptest.NewRecorder()}
	server.Handler().ServeHTTP(resp, req)
	body := resp.Body.String()
	if resp.Code != http.StatusOK || !strings.Contains(body, "id: 1") || !strings.Contains(body, "id: 2") || !strings.Contains(body, `"replayed_events":1`) {
		t.Fatalf("replayed SSE body = %s", body)
	}
	if !foundSessionAuditPayload(st, "mcp.session_resume", "session.replay", func(payload map[string]any) bool {
		if payload["client_last_event_id"] != "0" {
			return false
		}
		switch value := payload["replayed_events"].(type) {
		case int:
			return value == 1
		case float64:
			return value == 1
		default:
			return false
		}
	}) {
		t.Fatalf("expected replay count in session audit")
	}
}

func TestStreamableHTTPStreamFansOutPersistedEventsFromOtherReplica(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{MCPSessionTTL: time.Hour, MCPLiveFanoutInterval: 10 * time.Millisecond}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.fanout")
	resp, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	if body := readStreamUntil(t, reader, `"type":"ready"`, time.Second); !strings.Contains(body, "id: 1") {
		t.Fatalf("ready body = %s", body)
	}

	if _, err := st.AppendMCPSessionEvent("session.fanout", "external", map[string]any{"type": "external", "source": "replica-b"}); err != nil {
		t.Fatal(err)
	}
	body := readStreamUntil(t, reader, `"source":"replica-b"`, time.Second)
	if !strings.Contains(body, "id: 2") {
		t.Fatalf("fanout body = %s", body)
	}
	if !foundSessionAuditPayload(st, "mcp.session_fanout", "session.fanout", func(payload map[string]any) bool {
		switch value := payload["sent_events"].(type) {
		case int:
			return value == 1 && payload["last_event_id"] == "2"
		case float64:
			return value == 1 && payload["last_event_id"] == "2"
		default:
			return false
		}
	}) {
		t.Fatalf("expected fanout audit")
	}
}

func TestStreamableHTTPStreamFansOutPersistedEventsFromNotification(t *testing.T) {
	st := newNotifyingMemoryStore()
	server := NewServer(config.Config{MCPSessionTTL: time.Hour, MCPLiveFanoutInterval: time.Hour}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("MCP-Session-Id", "session.notify")
	resp, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	if body := readStreamUntil(t, reader, `"type":"ready"`, time.Second); !strings.Contains(body, "id: 1") {
		t.Fatalf("ready body = %s", body)
	}

	event, err := st.AppendMCPSessionEvent("session.notify", "external", map[string]any{"type": "external", "source": "notify"})
	if err != nil {
		t.Fatal(err)
	}
	st.publish(domain.MCPSessionEventNotification{SessionID: event.SessionID, Seq: event.Seq, EventType: event.EventType})
	body := readStreamUntil(t, reader, `"source":"notify"`, time.Second)
	if !strings.Contains(body, "id: 2") {
		t.Fatalf("notify fanout body = %s", body)
	}
	if !foundSessionAuditPayload(st.MemoryStore, "mcp.session_notify_fanout", "session.notify", func(payload map[string]any) bool {
		switch value := payload["sent_events"].(type) {
		case int:
			return value == 1 && payload["last_event_id"] == "2" && payload["fanout_transport"] == "postgres_listen_notify"
		case float64:
			return value == 1 && payload["last_event_id"] == "2" && payload["fanout_transport"] == "postgres_listen_notify"
		default:
			return false
		}
	}) {
		t.Fatalf("expected notify fanout audit")
	}
}

type notifyingMemoryStore struct {
	*store.MemoryStore
	notifications chan domain.MCPSessionEventNotification
}

func newNotifyingMemoryStore() *notifyingMemoryStore {
	return &notifyingMemoryStore{MemoryStore: store.NewMemoryStore(), notifications: make(chan domain.MCPSessionEventNotification, 10)}
}

func (s *notifyingMemoryStore) SubscribeMCPSessionEvents(ctx context.Context) (<-chan domain.MCPSessionEventNotification, func(), error) {
	return s.notifications, func() {}, nil
}

func (s *notifyingMemoryStore) publish(notification domain.MCPSessionEventNotification) {
	s.notifications <- notification
}

func readStreamUntil(t *testing.T, reader *bufio.Reader, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.After(timeout)
	var builder strings.Builder
	for {
		lineCh := make(chan string, 1)
		errCh := make(chan error, 1)
		go func() {
			line, err := reader.ReadString('\n')
			if err != nil {
				errCh <- err
				return
			}
			lineCh <- line
		}()
		select {
		case line := <-lineCh:
			builder.WriteString(line)
			if strings.Contains(builder.String(), needle) {
				return builder.String()
			}
		case err := <-errCh:
			t.Fatalf("stream read failed: %v, body = %s", err, builder.String())
		case <-deadline:
			t.Fatalf("timed out waiting for %q, body = %s", needle, builder.String())
		}
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (r *flushRecorder) Flush() {}

func TestStreamableHTTPRejectsRemoteOrigin(t *testing.T) {
	server := NewServer(config.Config{}, store.NewMemoryStore(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Origin", "https://evil.example")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
}

func TestStreamableHTTPInitializeConformanceShape(t *testing.T) {
	st := store.NewMemoryStore()
	server := NewServer(config.Config{}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"initialize"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
	}
	sessionID := resp.Header().Get("MCP-Session-Id")
	if resp.Header().Get("MCP-Protocol-Version") != "2025-06-18" || sessionID == "" {
		t.Fatalf("missing MCP headers: %#v", resp.Header())
	}
	if !strings.HasPrefix(sessionID, "mcp_session_") {
		t.Fatalf("session id = %q", sessionID)
	}
	var decoded map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded["jsonrpc"] != "2.0" || decoded["id"].(float64) != 7 {
		t.Fatalf("unexpected response envelope: %#v", decoded)
	}
	result := decoded["result"].(map[string]any)
	if result["protocolVersion"] != "2025-06-18" || result["serverInfo"] == nil || result["capabilities"] == nil {
		t.Fatalf("unexpected initialize result: %#v", result)
	}
	if !foundSessionAudit(st, "mcp.session_initialize", sessionID) {
		t.Fatalf("expected initialize session audit")
	}
}

func foundSessionAudit(st *store.MemoryStore, action string, sessionID string) bool {
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == action && entry.ResourceType == "mcp_session" && entry.ResourceID == sessionID {
			return true
		}
	}
	return false
}

func foundSessionAuditPayload(st *store.MemoryStore, action string, sessionID string, match func(map[string]any) bool) bool {
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == action && entry.ResourceType == "mcp_session" && entry.ResourceID == sessionID && match(entry.Payload) {
			return true
		}
	}
	return false
}
