package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	authn "github.com/Chiiz0/multi-codex/internal/auth"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/executor"
	"github.com/Chiiz0/multi-codex/internal/gitsync"
	"github.com/Chiiz0/multi-codex/internal/observability"
	"github.com/Chiiz0/multi-codex/internal/policy"
	"github.com/Chiiz0/multi-codex/internal/scheduler"
	"github.com/Chiiz0/multi-codex/internal/store"
	"github.com/Chiiz0/multi-codex/internal/workflow"
)

type Server struct {
	cfg     config.Config
	store   store.Store
	exec    *executor.Manager
	metrics *observability.Metrics
	oidc    *authn.OIDCVerifier
	log     *slog.Logger
}

type actorContextKey struct{}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewServer(cfg config.Config, st store.Store, log *slog.Logger) *Server {
	if cfg.MCPSessionTTL <= 0 {
		cfg.MCPSessionTTL = 8 * time.Hour
	}
	if cfg.MCPLiveFanoutInterval <= 0 {
		cfg.MCPLiveFanoutInterval = time.Second
	}
	var verifier *authn.OIDCVerifier
	if strings.EqualFold(cfg.AuthMode, "oidc") {
		verifier = authn.NewOIDCVerifier(cfg.OIDCIssuer, cfg.OIDCAudience, cfg.OIDCJWKSURL)
	}
	server := &Server{cfg: cfg, store: st, exec: executor.NewManager(cfg, st), metrics: observability.NewMetrics("multi-codex-mcp-gateway"), oidc: verifier, log: log}
	server.startTelemetryPushWorker()
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /metrics", s.metricsEndpoint)
	mux.HandleFunc("GET /tools", s.tools)
	mux.HandleFunc("POST /tools/call", s.callTool)
	mux.HandleFunc("GET /mcp", s.mcpStream)
	mux.HandleFunc("POST /mcp", s.mcpPost)
	return s.metrics.Middleware(originGuard(s.withAuth(mux)))
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "multi-codex-mcp-gateway"})
}

func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if observability.WantsOTLP(r) {
		writeJSON(w, http.StatusOK, s.metrics.OTLPExport(s.store.ListAllRuns()))
		return
	}
	if observability.WantsPrometheus(r) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(s.metrics.PrometheusText()))
		_, _ = w.Write([]byte(observability.RunsPrometheusText("multi-codex-mcp-gateway", s.store.ListAllRuns())))
		return
	}
	snapshot := s.metrics.Snapshot()
	snapshot["runs"] = observability.RunMetricsSnapshot(s.store.ListAllRuns())
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) startTelemetryPushWorker() {
	if s.cfg.TelemetryPushURL == "" {
		return
	}
	if s.cfg.TelemetryPushInterval <= 0 {
		s.log.Error("telemetry push worker disabled by invalid interval", "interval", s.cfg.TelemetryPushInterval)
		return
	}
	go func() {
		ticker := time.NewTicker(s.cfg.TelemetryPushInterval)
		defer ticker.Stop()
		s.log.Info("telemetry push worker started", "interval", s.cfg.TelemetryPushInterval, "url", s.cfg.TelemetryPushURL)
		for range ticker.C {
			s.pushTelemetry("scheduled")
		}
	}()
}

func (s *Server) pushTelemetry(trigger string) {
	payload := s.metrics.OTLPExport(s.store.ListAllRuns())
	data, err := json.Marshal(payload)
	if err != nil {
		s.recordTelemetryPushFailure(trigger, map[string]any{"error": err.Error()})
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.cfg.TelemetryPushURL, bytes.NewReader(data))
	if err != nil {
		s.recordTelemetryPushFailure(trigger, map[string]any{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		s.recordTelemetryPushFailure(trigger, map[string]any{"error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		s.recordTelemetryPushFailure(trigger, map[string]any{"status": resp.StatusCode})
	}
}

func (s *Server) recordTelemetryPushFailure(trigger string, payload map[string]any) {
	payload["trigger"] = trigger
	s.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "system",
		ActorID:      "mcp",
		Action:       "mcp.telemetry_push_failed",
		ResourceType: "telemetry",
		ResourceID:   "otlp",
		Payload:      payload,
	})
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(s.cfg.AuthMode, "oidc") || r.URL.Path == "/healthz" || r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		if s.oidc == nil {
			writeError(w, http.StatusUnauthorized, "OIDC verifier is not configured")
			return
		}
		token, ok := authn.BearerToken(r.Header.Get("Authorization"))
		if !ok {
			s.store.RecordAuditLog(domain.AuditLog{
				ActorType:    "anonymous",
				ActorID:      "anonymous",
				Action:       "mcp.auth_denied",
				ResourceType: "http_request",
				ResourceID:   r.URL.Path,
				Payload: map[string]any{
					"method":   r.Method,
					"path":     r.URL.Path,
					"mode":     s.cfg.AuthMode,
					"error":    authn.ErrMissingBearer.Error(),
					"trace_id": observability.TraceID(r.Context()),
				},
			})
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		tokenHash := authn.TokenHash(token)
		if s.store.IsAuthTokenRevoked(tokenHash, time.Now().UTC()) {
			s.store.RecordAuditLog(domain.AuditLog{
				ActorType:    "anonymous",
				ActorID:      "anonymous",
				Action:       "mcp.auth_denied",
				ResourceType: "http_request",
				ResourceID:   r.URL.Path,
				Payload: map[string]any{
					"method":            r.Method,
					"path":              r.URL.Path,
					"mode":              s.cfg.AuthMode,
					"error":             "bearer token revoked",
					"token_hash_prefix": hashPrefix(tokenHash),
					"trace_id":          observability.TraceID(r.Context()),
				},
			})
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		claims, err := s.oidc.VerifyToken(r.Context(), token)
		if err != nil {
			s.store.RecordAuditLog(domain.AuditLog{
				ActorType:    "anonymous",
				ActorID:      "anonymous",
				Action:       "mcp.auth_denied",
				ResourceType: "http_request",
				ResourceID:   r.URL.Path,
				Payload: map[string]any{
					"method":   r.Method,
					"path":     r.URL.Path,
					"mode":     s.cfg.AuthMode,
					"error":    err.Error(),
					"trace_id": observability.TraceID(r.Context()),
				},
			})
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		mapped := authn.MapIdentity(claims, authn.IdentityMapping{
			DefaultRole:       s.cfg.OIDCDefaultRole,
			DefaultOrgID:      s.cfg.OIDCDefaultOrgID,
			GroupRoleMappings: s.cfg.OIDCGroupRoleMap,
			GroupOrgMappings:  s.cfg.OIDCGroupOrgMap,
		})
		authCtx, err := s.store.UpsertExternalUser("oidc", claims.Subject, claims.Email, mapped.DisplayName, mapped.Role, mapped.OrgID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		s.store.RecordAuditLog(domain.AuditLog{
			ActorType:    "human",
			ActorID:      authCtx.User.ID,
			Action:       "mcp.auth_oidc_mapped",
			ResourceType: "user",
			ResourceID:   authCtx.User.ID,
			Payload: map[string]any{
				"subject":            claims.Subject,
				"issuer":             claims.Issuer,
				"role":               authCtx.Membership.Role,
				"org_id":             authCtx.Membership.OrgID,
				"matched_role_claim": mapped.MatchedRoleClaim,
				"matched_org_claim":  mapped.MatchedOrgClaim,
			},
		})
		ctx := context.WithValue(r.Context(), actorContextKey{}, authCtx.User.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) tools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.toolCatalog())
}

func (s *Server) toolCatalog() []Tool {
	return []Tool{
		{
			Name:        "organization_list",
			Description: "List provisioned organizations available for OIDC mapping and project ownership.",
			InputSchema: objectSchema(),
		},
		{
			Name:        "organization_create",
			Description: "Provision an organization for OIDC group mapping and enterprise project ownership.",
			InputSchema: objectSchema("name", "slug"),
		},
		{
			Name:        "policy_validate_task",
			Description: "Validate a Task Envelope before a worker can be spawned.",
			InputSchema: objectSchema("task_envelope"),
		},
		{
			Name:        "task_create",
			Description: "Create a task from a validated Task Envelope.",
			InputSchema: objectSchema("task_envelope"),
		},
		{
			Name:        "task_list",
			Description: "List tasks for a project.",
			InputSchema: objectSchema("project_id"),
		},
		{
			Name:        "task_get",
			Description: "Return a stored task by internal task id.",
			InputSchema: objectSchema("task_id"),
		},
		{
			Name:        "worker_spawn",
			Description: "Create a run record for a role-specific worker after policy validation.",
			InputSchema: objectSchema("task_id", "role", "executor"),
		},
		{
			Name:        "worker_status",
			Description: "Return run status and collected run events.",
			InputSchema: objectSchema("run_id"),
		},
		{
			Name:        "worker_logs",
			Description: "Return collected run events as worker logs.",
			InputSchema: objectSchema("run_id"),
		},
		{
			Name:        "worker_result",
			Description: "Record a worker result and transition the run to a terminal state.",
			InputSchema: objectSchema("run_id", "status", "result"),
		},
		{
			Name:        "queue_status",
			Description: "Return queued worker runs and executor backpressure snapshots.",
			InputSchema: objectSchema(),
		},
		{
			Name:        "queue_dispatch",
			Description: "Dispatch one queued worker run through the same capacity checks used by the API scheduler.",
			InputSchema: objectSchema(),
		},
		{
			Name:        "repo_scope_check",
			Description: "Check changed files against a task's allowed and forbidden paths.",
			InputSchema: objectSchema("task_id", "changed_files"),
		},
		{
			Name:        "test_run_required",
			Description: "Start the required independent test worker if workflow gates allow it.",
			InputSchema: objectSchema("task_id"),
		},
		{
			Name:        "audit_run",
			Description: "Start the required read-only audit worker if workflow gates allow it.",
			InputSchema: objectSchema("task_id"),
		},
		{
			Name:        "approval_request",
			Description: "Create a human approval request for a gated operation.",
			InputSchema: objectSchema("task_id", "approval_type"),
		},
		{
			Name:        "approval_status",
			Description: "List approval requests for a task.",
			InputSchema: objectSchema("task_id"),
		},
		{
			Name:        "git_prepare_pr",
			Description: "Prepare PR body material after scope, test, audit, and approval gates pass.",
			InputSchema: objectSchema("task_id"),
		},
		{
			Name:        "git_publish_pr",
			Description: "Prepare or create a provider PR operation after explicit pr_publish approval; never auto-merges.",
			InputSchema: objectSchema("task_id"),
		},
	}
}

func (s *Server) callTool(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req struct {
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	output, status := s.invokeTool(r.Context(), req.Name, req.Input)
	writeJSON(w, status, output)
}

func (s *Server) initializeSession(id string, actorID string, protocolVersion string, now time.Time) (domain.MCPSession, error) {
	if protocolVersion == "" {
		protocolVersion = "2025-06-18"
	}
	session := domain.MCPSession{
		ID:              id,
		ActorID:         actorID,
		ProtocolVersion: protocolVersion,
		Status:          "active",
		CreatedAt:       now,
		LastSeenAt:      now,
		ExpiresAt:       now.Add(s.cfg.MCPSessionTTL),
	}
	if existing, err := s.store.GetMCPSession(id); err == nil {
		session.CreatedAt = existing.CreatedAt
		session.LastEventID = existing.LastEventID
	}
	return s.store.UpsertMCPSession(session)
}

func (s *Server) touchSession(id string, actorID string, now time.Time) (domain.MCPSession, bool, error) {
	session, err := s.store.GetMCPSession(id)
	if errors.Is(err, store.ErrNotFound) {
		session = domain.MCPSession{
			ID:              id,
			ActorID:         actorID,
			ProtocolVersion: "2025-06-18",
			Status:          "active",
			CreatedAt:       now,
			LastSeenAt:      now,
			ExpiresAt:       now.Add(s.cfg.MCPSessionTTL),
		}
		session, err = s.store.UpsertMCPSession(session)
		return session, false, err
	}
	if err != nil {
		return domain.MCPSession{}, false, err
	}
	if session.Status == "active" && now.After(session.ExpiresAt) {
		session.Status = "expired"
		session.LastSeenAt = now
		session, err = s.store.UpsertMCPSession(session)
		return session, true, err
	}
	if session.Status == "expired" {
		return session, true, nil
	}
	if session.ProtocolVersion == "" {
		session.ProtocolVersion = "2025-06-18"
	}
	session.ActorID = actorID
	session.Status = "active"
	session.LastSeenAt = now
	session.ExpiresAt = now.Add(s.cfg.MCPSessionTTL)
	session, err = s.store.UpsertMCPSession(session)
	return session, false, err
}

func (s *Server) ensureSessionEventFloor(session domain.MCPSession, floor int64, now time.Time) (domain.MCPSession, error) {
	if floor <= session.LastEventID {
		return session, nil
	}
	session.LastEventID = floor
	session.LastSeenAt = now
	session.ExpiresAt = now.Add(s.cfg.MCPSessionTTL)
	return s.store.UpsertMCPSession(session)
}

func (s *Server) appendSessionStreamEvent(sessionID string, eventType string, payload map[string]any) (domain.MCPSessionEvent, error) {
	return s.store.AppendMCPSessionEvent(sessionID, eventType, payload)
}

func (s *Server) mcpStream(w http.ResponseWriter, r *http.Request) {
	if !acceptsMedia(r.Header.Get("Accept"), "text/event-stream") {
		writeError(w, http.StatusNotAcceptable, "Accept must include text/event-stream")
		return
	}
	sessionID := mcpSessionID(r)
	now := time.Now().UTC()
	session, expired, err := s.touchSession(sessionID, mcpActorID(r.Context()), now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP session persistence failed")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("MCP-Protocol-Version", "2025-06-18")
	w.Header().Set("MCP-Session-Id", sessionID)
	w.Header().Set("MCP-Session-Expires-At", session.ExpiresAt.Format(time.RFC3339Nano))
	if expired {
		s.recordSessionEvent(r.Context(), "mcp.session_expired", sessionID, sessionAuditPayload(session, map[string]any{"method": r.Method, "path": r.URL.Path}))
		writeError(w, http.StatusNotFound, "MCP session expired")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	resumed := lastEventID != ""
	clientLastEventID := parseLastEventID(lastEventID)
	if clientLastEventID > 0 {
		session, err = s.ensureSessionEventFloor(session, clientLastEventID, time.Now().UTC())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "MCP session persistence failed")
			return
		}
	}
	replayedEvents := s.store.ListMCPSessionEventsAfter(sessionID, clientLastEventID, 100)
	for _, event := range replayedEvents {
		writeSSE(w, event.Seq, event.Payload)
	}
	readyPayload := map[string]any{
		"type":            "ready",
		"server":          "multi-codex-mcp-gateway",
		"resumed":         resumed,
		"replayed_events": len(replayedEvents),
	}
	readyEvent, err := s.appendSessionStreamEvent(sessionID, "ready", readyPayload)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "MCP session event persistence failed")
		return
	}
	session.LastEventID = readyEvent.Seq
	payload := sessionAuditPayload(session, map[string]any{
		"method":               r.Method,
		"path":                 r.URL.Path,
		"client_last_event_id": lastEventID,
		"last_event_id":        strconv.FormatInt(session.LastEventID, 10),
		"replayed_events":      len(replayedEvents),
		"resumed":              resumed,
		"fanout_interval_ms":   s.cfg.MCPLiveFanoutInterval.Milliseconds(),
	})
	if resumed {
		s.recordSessionEvent(r.Context(), "mcp.session_resume", sessionID, payload)
	}
	s.recordSessionEvent(r.Context(), "mcp.session_stream_open", sessionID, payload)
	writeSSE(w, readyEvent.Seq, readyEvent.Payload)
	flusher.Flush()
	lastSentEventID := readyEvent.Seq
	notificationCh, cleanupNotifications := s.subscribeSessionNotifications(r.Context(), session, sessionID)
	defer cleanupNotifications()

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()
	fanoutTicker := time.NewTicker(s.cfg.MCPLiveFanoutInterval)
	defer fanoutTicker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case notification, ok := <-notificationCh:
			if !ok {
				notificationCh = nil
				continue
			}
			if notification.SessionID != sessionID || notification.Seq <= lastSentEventID {
				continue
			}
			sent := s.flushSessionStreamEvents(w, sessionID, &lastSentEventID, 100)
			if sent > 0 {
				s.recordSessionEvent(r.Context(), "mcp.session_notify_fanout", sessionID, sessionAuditPayload(session, map[string]any{
					"sent_events":         sent,
					"last_event_id":       strconv.FormatInt(lastSentEventID, 10),
					"notified_event_id":   strconv.FormatInt(notification.Seq, 10),
					"notified_event_type": notification.EventType,
					"fanout_transport":    "postgres_listen_notify",
				}))
				flusher.Flush()
			}
		case <-fanoutTicker.C:
			sent := s.flushSessionStreamEvents(w, sessionID, &lastSentEventID, 100)
			if sent > 0 {
				s.recordSessionEvent(r.Context(), "mcp.session_fanout", sessionID, sessionAuditPayload(session, map[string]any{
					"sent_events":        sent,
					"last_event_id":      strconv.FormatInt(lastSentEventID, 10),
					"fanout_interval_ms": s.cfg.MCPLiveFanoutInterval.Milliseconds(),
				}))
				flusher.Flush()
			}
		case now := <-heartbeatTicker.C:
			if _, expired, err := s.touchSession(sessionID, mcpActorID(r.Context()), now.UTC()); err != nil || expired {
				return
			}
			s.flushSessionStreamEvents(w, sessionID, &lastSentEventID, 100)
			heartbeat, err := s.appendSessionStreamEvent(sessionID, "heartbeat", map[string]any{"type": "heartbeat", "created_at": now.UTC()})
			if err != nil {
				return
			}
			writeSSE(w, heartbeat.Seq, heartbeat.Payload)
			lastSentEventID = heartbeat.Seq
			flusher.Flush()
		}
	}
}

func (s *Server) subscribeSessionNotifications(ctx context.Context, session domain.MCPSession, sessionID string) (<-chan domain.MCPSessionEventNotification, func()) {
	subscriber, ok := s.store.(store.MCPSessionEventSubscriber)
	if !ok {
		return nil, func() {}
	}
	notifications, cleanup, err := subscriber.SubscribeMCPSessionEvents(ctx)
	if err != nil {
		s.recordSessionEvent(ctx, "mcp.session_notify_subscribe_failed", sessionID, sessionAuditPayload(session, map[string]any{
			"error": err.Error(),
		}))
		return nil, func() {}
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	return notifications, cleanup
}

func (s *Server) flushSessionStreamEvents(w http.ResponseWriter, sessionID string, lastSentEventID *int64, limit int) int {
	events := s.store.ListMCPSessionEventsAfter(sessionID, *lastSentEventID, limit)
	for _, event := range events {
		writeSSE(w, event.Seq, event.Payload)
		*lastSentEventID = event.Seq
	}
	return len(events)
}

func (s *Server) mcpPost(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sessionID := mcpSessionID(r)
	w.Header().Set("MCP-Protocol-Version", "2025-06-18")
	w.Header().Set("MCP-Session-Id", sessionID)
	if !acceptsMedia(r.Header.Get("Accept"), "application/json") || !acceptsMedia(r.Header.Get("Accept"), "text/event-stream") {
		writeJSON(w, http.StatusNotAcceptable, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32000, Message: "Accept must include application/json and text/event-stream"}})
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}})
		return
	}
	now := time.Now().UTC()
	var session domain.MCPSession
	if req.ID == nil && strings.HasPrefix(req.Method, "notifications/") {
		var expired bool
		var err error
		session, expired, err = s.touchSession(sessionID, mcpActorID(r.Context()), now)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32002, Message: "MCP session persistence failed"}})
			return
		}
		w.Header().Set("MCP-Session-Expires-At", session.ExpiresAt.Format(time.RFC3339Nano))
		if expired {
			s.recordSessionEvent(r.Context(), "mcp.session_expired", sessionID, sessionAuditPayload(session, map[string]any{"method": req.Method}))
			writeJSON(w, http.StatusNotFound, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32001, Message: "MCP session expired"}})
			return
		}
		s.recordSessionEvent(r.Context(), "mcp.session_notification", sessionID, sessionAuditPayload(session, map[string]any{"method": req.Method}))
		w.WriteHeader(http.StatusAccepted)
		return
	}

	switch req.Method {
	case "initialize":
		var err error
		session, err = s.initializeSession(sessionID, mcpActorID(r.Context()), "2025-06-18", now)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32002, Message: "MCP session persistence failed"}})
			return
		}
		w.Header().Set("MCP-Session-Expires-At", session.ExpiresAt.Format(time.RFC3339Nano))
		s.recordSessionEvent(r.Context(), "mcp.session_initialize", sessionID, sessionAuditPayload(session, map[string]any{"method": req.Method}))
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "multi-codex-mcp-gateway", "version": "0.1.0"},
		}})
	case "ping":
		if !s.touchPostSession(w, r, sessionID, req.Method) {
			return
		}
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		if !s.touchPostSession(w, r, sessionID, req.Method) {
			return
		}
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": s.toolCatalog()}})
	case "tools/call":
		if !s.touchPostSession(w, r, sessionID, req.Method) {
			return
		}
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
			writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "invalid tools/call params"}})
			return
		}
		if len(params.Arguments) == 0 {
			params.Arguments = []byte(`{}`)
		}
		output, status := s.invokeTool(r.Context(), params.Name, params.Arguments)
		if status >= 400 {
			writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32000, Message: valueToText(output)}})
			return
		}
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: toolResult(output)})
	default:
		if !s.touchPostSession(w, r, sessionID, req.Method) {
			return
		}
		writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found"}})
	}
}

func (s *Server) touchPostSession(w http.ResponseWriter, r *http.Request, sessionID string, method string) bool {
	session, expired, err := s.touchSession(sessionID, mcpActorID(r.Context()), time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32002, Message: "MCP session persistence failed"}})
		return false
	}
	w.Header().Set("MCP-Session-Expires-At", session.ExpiresAt.Format(time.RFC3339Nano))
	if !expired {
		return true
	}
	s.recordSessionEvent(r.Context(), "mcp.session_expired", sessionID, sessionAuditPayload(session, map[string]any{"method": method}))
	writeJSON(w, http.StatusNotFound, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32001, Message: "MCP session expired"}})
	return false
}

func (s *Server) recordSessionEvent(ctx context.Context, action string, sessionID string, payload map[string]any) {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["trace_id"] = observability.TraceID(ctx)
	s.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "mcp",
		ActorID:      mcpActorID(ctx),
		Action:       action,
		ResourceType: "mcp_session",
		ResourceID:   sessionID,
		Payload:      payload,
	})
}

func sessionAuditPayload(session domain.MCPSession, extra map[string]any) map[string]any {
	payload := map[string]any{
		"status":     session.Status,
		"expires_at": session.ExpiresAt.Format(time.RFC3339Nano),
	}
	if !session.LastSeenAt.IsZero() {
		payload["last_seen_at"] = session.LastSeenAt.Format(time.RFC3339Nano)
	}
	if session.LastEventID > 0 {
		payload["last_event_id"] = strconv.FormatInt(session.LastEventID, 10)
	}
	for k, v := range extra {
		payload[k] = v
	}
	return payload
}

func (s *Server) invokeTool(ctx context.Context, name string, inputRaw json.RawMessage) (any, int) {
	if len(inputRaw) == 0 {
		inputRaw = []byte(`{}`)
	}
	switch name {
	case "organization_list":
		output := map[string]any{"organizations": s.store.ListOrganizations()}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "organization", "")
		return output, http.StatusOK
	case "organization_create":
		var input struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "organization", input.Slug)
			return output, http.StatusBadRequest
		}
		if input.Name == "" || input.Slug == "" {
			output := map[string]any{"error": "name and slug are required"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "organization", input.Slug)
			return output, http.StatusBadRequest
		}
		org, err := s.store.CreateOrganization(domain.Organization{Name: input.Name, Slug: input.Slug})
		if err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "organization", input.Slug)
			return output, storeErrorStatus(err)
		}
		output := map[string]any{"organization": org}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "organization", org.ID)
		return output, http.StatusCreated
	case "policy_validate_task":
		var input struct {
			TaskEnvelope domain.TaskEnvelope `json:"task_envelope"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", "")
			return output, http.StatusBadRequest
		}
		result := policy.ValidateTaskWithResources(s.store, input.TaskEnvelope)
		s.recordTool(ctx, name, inputRaw, result, statusName(result.Valid), "task", input.TaskEnvelope.TaskID)
		return result, http.StatusOK
	case "task_create":
		var input struct {
			TaskEnvelope domain.TaskEnvelope `json:"task_envelope"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", "")
			return output, http.StatusBadRequest
		}
		validation := policy.ValidateTaskWithResources(s.store, input.TaskEnvelope)
		if !validation.Valid {
			output := map[string]any{"validation": validation}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskEnvelope.TaskID)
			return output, http.StatusBadRequest
		}
		task := s.store.CreateTask(input.TaskEnvelope)
		output := map[string]any{"task": task, "validation": validation}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "task", task.ID)
		return output, http.StatusCreated
	case "task_list":
		var input struct {
			ProjectID string `json:"project_id"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "project", input.ProjectID)
			return output, http.StatusBadRequest
		}
		output := map[string]any{"tasks": s.store.ListTasks(input.ProjectID)}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "project", input.ProjectID)
		return output, http.StatusOK
	case "task_get":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusBadRequest
		}
		task, err := s.store.GetTask(input.TaskID)
		if err != nil {
			output := map[string]any{"error": "task not found"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusNotFound
		}
		s.recordTool(ctx, name, inputRaw, task, "succeeded", "task", task.ID)
		return task, http.StatusOK
	case "worker_spawn":
		var input struct {
			TaskID   string `json:"task_id"`
			Role     string `json:"role"`
			Executor string `json:"executor"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusBadRequest
		}
		task, err := s.store.GetTask(input.TaskID)
		if err != nil {
			output := map[string]any{"error": "task not found"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusNotFound
		}
		validation := policy.ValidateTaskWithResources(s.store, task.Envelope)
		if !validation.Valid {
			output := map[string]any{"validation": validation}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusConflict
		}
		state := workflow.BuildState(s.store, task)
		if ok, reasons := workflow.GateAllows(state, "worker_spawn"); !ok {
			output := map[string]any{"workflow": state, "blocked_reasons": reasons}
			s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
			return output, http.StatusConflict
		}
		if input.Role == "" {
			input.Role = task.Envelope.Role
		}
		if input.Executor == "" {
			input.Executor = task.Envelope.Executor
		}
		run, err := s.store.StartRun(input.TaskID, input.Role, input.Executor)
		if err != nil {
			output, toolStatus, httpStatus := s.startRunErrorOutput(ctx, err, task, input.Role, input.Executor)
			s.recordTool(ctx, name, inputRaw, output, toolStatus, "task", input.TaskID)
			return output, httpStatus
		}
		output := map[string]any{"run": run}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", run.ID)
		s.exec.Start(ctx, task, run)
		return output, http.StatusAccepted
	case "worker_status":
		var input struct {
			RunID string `json:"run_id"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "run", input.RunID)
			return output, http.StatusBadRequest
		}
		run, err := s.store.GetRun(input.RunID)
		if err != nil {
			output := map[string]any{"error": "run not found"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "run", input.RunID)
			return output, http.StatusNotFound
		}
		output := map[string]any{"run": run, "events": s.store.ListEvents(input.RunID)}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", input.RunID)
		return output, http.StatusOK
	case "worker_logs":
		var input struct {
			RunID string `json:"run_id"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "run", input.RunID)
			return output, http.StatusBadRequest
		}
		output := map[string]any{"events": s.store.ListEvents(input.RunID)}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", input.RunID)
		return output, http.StatusOK
	case "worker_result":
		var input struct {
			RunID  string         `json:"run_id"`
			Status string         `json:"status"`
			Result map[string]any `json:"result"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "run", input.RunID)
			return output, http.StatusBadRequest
		}
		if input.Result == nil {
			input.Result = map[string]any{}
		}
		run, err := s.store.FinishRun(input.RunID, input.Status, input.Result)
		if err != nil {
			output := map[string]any{"error": "run not found"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "run", input.RunID)
			return output, http.StatusNotFound
		}
		output := map[string]any{"run": run}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", input.RunID)
		return output, http.StatusOK
	case "queue_status":
		output := s.queueSnapshot()
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "queue", "runs")
		return output, http.StatusOK
	case "queue_dispatch":
		run, err := s.dispatchOneQueuedRun(ctx, "manual")
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				output := map[string]any{"error": "no queued run available", "queue": s.queueSnapshot()}
				s.recordTool(ctx, name, inputRaw, output, "blocked", "queue", "runs")
				return output, http.StatusNotFound
			}
			if errors.Is(err, store.ErrNoCapacity) {
				output := map[string]any{"error": "no executor capacity available", "queue": s.queueSnapshot()}
				s.recordTool(ctx, name, inputRaw, output, "blocked", "queue", "runs")
				return output, http.StatusConflict
			}
			output := map[string]any{"error": err.Error(), "queue": s.queueSnapshot()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "queue", "runs")
			return output, storeErrorStatus(err)
		}
		output := map[string]any{"run": run, "queue": s.queueSnapshot()}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", run.ID)
		return output, http.StatusAccepted
	case "repo_scope_check":
		var input struct {
			TaskID       string   `json:"task_id"`
			ChangedFiles []string `json:"changed_files"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusBadRequest
		}
		task, err := s.store.GetTask(input.TaskID)
		if err != nil {
			output := map[string]any{"error": "task not found"}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusNotFound
		}
		result := policy.CheckScope(input.ChangedFiles, task.Envelope.AllowedPaths, task.Envelope.ForbiddenPaths)
		record, err := s.store.RecordScopeCheck(input.TaskID, "", task.Envelope.BaseBranch, result)
		if err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusInternalServerError
		}
		if result.Status == "blocked" {
			_, _ = s.store.UpdateTaskStatus(input.TaskID, "blocked")
		}
		output := map[string]any{"scope_check": record, "result": result}
		s.recordTool(ctx, name, inputRaw, output, result.Status, "task", input.TaskID)
		return output, http.StatusOK
	case "test_run_required":
		return s.startWorkflowRun(ctx, name, inputRaw, "test")
	case "audit_run":
		return s.startWorkflowRun(ctx, name, inputRaw, "audit")
	case "approval_request":
		var input struct {
			TaskID       string `json:"task_id"`
			ApprovalType string `json:"approval_type"`
			Reason       string `json:"reason"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusBadRequest
		}
		if input.ApprovalType == "" {
			input.ApprovalType = "pr_prepare"
		}
		approval, err := s.store.CreateApproval(domain.Approval{TaskID: input.TaskID, ApprovalType: input.ApprovalType, Status: "pending", Reason: input.Reason})
		if err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusNotFound
		}
		output := map[string]any{"approval": approval}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "approval", approval.ID)
		return output, http.StatusCreated
	case "approval_status":
		var input struct {
			TaskID string `json:"task_id"`
		}
		if err := decodeToolInput(inputRaw, &input); err != nil {
			output := map[string]any{"error": err.Error()}
			s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
			return output, http.StatusBadRequest
		}
		output := map[string]any{"approvals": s.store.ListApprovals(input.TaskID)}
		s.recordTool(ctx, name, inputRaw, output, "succeeded", "task", input.TaskID)
		return output, http.StatusOK
	case "git_prepare_pr":
		return s.preparePR(ctx, name, inputRaw)
	case "git_publish_pr":
		return s.publishPR(ctx, name, inputRaw)
	default:
		output := map[string]any{"error": "unknown tool"}
		s.recordTool(ctx, name, inputRaw, output, "failed", "tool", name)
		return output, http.StatusNotFound
	}
}

func (s *Server) startWorkflowRun(ctx context.Context, name string, inputRaw json.RawMessage, role string) (any, int) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeToolInput(inputRaw, &input); err != nil {
		output := map[string]any{"error": err.Error()}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusBadRequest
	}
	task, err := s.store.GetTask(input.TaskID)
	if err != nil {
		output := map[string]any{"error": "task not found"}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusNotFound
	}
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, name); !ok {
		output := map[string]any{"workflow": state, "blocked_reasons": reasons}
		s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
		return output, http.StatusConflict
	}
	run, err := s.store.StartRun(input.TaskID, role, task.Envelope.Executor)
	if err != nil {
		output, toolStatus, httpStatus := s.startRunErrorOutput(ctx, err, task, role, task.Envelope.Executor)
		s.recordTool(ctx, name, inputRaw, output, toolStatus, "task", input.TaskID)
		return output, httpStatus
	}
	_, _ = s.store.AddEvent(run.ID, "info", "workflow_gate", "MCP Gateway started role run", map[string]any{"role": role})
	output := map[string]any{"run": run}
	s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", run.ID)
	s.exec.Start(ctx, task, run)
	return output, http.StatusAccepted
}

func (s *Server) preparePR(ctx context.Context, name string, inputRaw json.RawMessage) (any, int) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeToolInput(inputRaw, &input); err != nil {
		output := map[string]any{"error": err.Error()}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusBadRequest
	}
	task, err := s.store.GetTask(input.TaskID)
	if err != nil {
		output := map[string]any{"error": "task not found"}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusNotFound
	}
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, "git_prepare_pr"); !ok {
		output := map[string]any{"workflow": state, "blocked_reasons": reasons}
		s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
		return output, http.StatusConflict
	}
	run, err := s.store.StartRun(input.TaskID, "git_sync", task.Envelope.Executor)
	if err != nil {
		output, toolStatus, httpStatus := s.startRunErrorOutput(ctx, err, task, "git_sync", task.Envelope.Executor)
		s.recordTool(ctx, name, inputRaw, output, toolStatus, "task", input.TaskID)
		return output, httpStatus
	}
	prBody := workflow.RenderPRBody(task, state)
	repo, _ := s.store.GetRepository(task.RepositoryID)
	result := map[string]any{
		"status":      "prepared",
		"summary":     "PR body prepared after scope, test, audit, and approval gates.",
		"pr_body":     prBody,
		"allow_push":  false,
		"auto_merge":  false,
		"needs_human": []string{"Review the PR body and explicitly approve any push/PR operation."},
	}
	_, _ = s.store.AddEvent(run.ID, "info", "git_prepare_pr", "Git Sync prepared PR body without pushing or merging", map[string]any{"allow_push": false})
	artifact, _ := s.store.CreateArtifact(domain.Artifact{
		RunID: run.ID,
		Kind:  "pr_body",
		Name:  "pr-body.md",
		Path:  "memory://pr-body/" + run.ID,
		Metadata: map[string]any{
			"content": prBody,
		},
	})
	result["pr_publish_plan"] = workflow.RenderPRPublishPlan(task, repo, artifact.ID)
	_, _ = s.store.FinishRun(run.ID, "succeeded", result)
	output := map[string]any{"run": run, "result": result, "artifact": artifact}
	s.recordTool(ctx, name, inputRaw, output, "succeeded", "run", run.ID)
	return output, http.StatusCreated
}

func (s *Server) publishPR(ctx context.Context, name string, inputRaw json.RawMessage) (any, int) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := decodeToolInput(inputRaw, &input); err != nil {
		output := map[string]any{"error": err.Error()}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusBadRequest
	}
	task, err := s.store.GetTask(input.TaskID)
	if err != nil {
		output := map[string]any{"error": "task not found"}
		s.recordTool(ctx, name, inputRaw, output, "failed", "task", input.TaskID)
		return output, http.StatusNotFound
	}
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, "git_publish_pr"); !ok {
		output := map[string]any{"workflow": state, "blocked_reasons": reasons}
		s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
		return output, http.StatusConflict
	}
	preparedRun := workflow.LatestRunWithResultStatus(state.Runs, "git_sync", "prepared")
	if preparedRun == nil {
		output := map[string]any{"error": "PR body has not been prepared"}
		s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
		return output, http.StatusConflict
	}
	prBody, _ := preparedRun.Result["pr_body"].(string)
	if prBody == "" {
		output := map[string]any{"error": "prepared PR body is missing"}
		s.recordTool(ctx, name, inputRaw, output, "blocked", "task", input.TaskID)
		return output, http.StatusConflict
	}
	repo, err := s.store.GetRepository(task.RepositoryID)
	if err != nil {
		output := map[string]any{"error": "repository not found"}
		s.recordTool(ctx, name, inputRaw, output, "failed", "repository", task.RepositoryID)
		return output, http.StatusNotFound
	}
	run, err := s.store.StartRun(input.TaskID, "git_sync", task.Envelope.Executor)
	if err != nil {
		output, toolStatus, httpStatus := s.startRunErrorOutput(ctx, err, task, "git_sync", task.Envelope.Executor)
		s.recordTool(ctx, name, inputRaw, output, toolStatus, "task", input.TaskID)
		return output, httpStatus
	}
	result := gitsync.PublishPR(ctx, gitsync.PublishRequest{
		Task:           task,
		Repository:     repo,
		Body:           prBody,
		BodyArtifactID: bodyArtifactID(preparedRun.Result),
		Config:         s.cfg,
	})
	runStatus := "succeeded"
	if result.Status == "blocked" {
		runStatus = "blocked"
	}
	resultMap := map[string]any{
		"status":            result.Status,
		"summary":           result.Summary,
		"publish_result":    result,
		"auto_merge":        false,
		"required_approval": "pr_publish",
	}
	_, _ = s.store.AddEvent(run.ID, "info", "git_publish_pr", "Git Sync prepared or created PR operation without auto-merge", map[string]any{
		"provider":            result.Provider,
		"credential_provider": result.CredentialProvider,
		"credential_resolved": result.CredentialResolved,
		"dry_run":             result.DryRun,
		"status":              result.Status,
		"auto_merge":          false,
	})
	finished, _ := s.store.FinishRun(run.ID, runStatus, resultMap)
	output := map[string]any{"run": finished, "result": result}
	s.recordTool(ctx, name, inputRaw, output, statusForRunStatus(runStatus), "run", run.ID)
	s.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "codex",
		ActorID:      mcpActorID(ctx),
		Action:       "mcp.git_publish_pr",
		ResourceType: "run",
		ResourceID:   run.ID,
		Payload: map[string]any{
			"task_id":             task.ID,
			"status":              result.Status,
			"dry_run":             result.DryRun,
			"provider":            result.Provider,
			"credential_provider": result.CredentialProvider,
			"credential_resolved": result.CredentialResolved,
			"auto_merge":          false,
		},
	})
	if runStatus == "blocked" {
		return output, http.StatusConflict
	}
	return output, http.StatusCreated
}

func (s *Server) recordTool(ctx context.Context, name string, input json.RawMessage, output any, status string, resourceType string, resourceID string) {
	s.store.RecordToolCall(domain.ToolCall{
		Caller:   "mcp-gateway",
		ToolName: name,
		Input:    rawToMap(input),
		Output:   valueToMap(output),
		Status:   status,
	})
	s.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "codex",
		ActorID:      mcpActorID(ctx),
		Action:       "mcp_tool." + name,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Payload: map[string]any{
			"status": status,
		},
	})
}

func (s *Server) dispatchOneQueuedRun(ctx context.Context, trigger string) (domain.Run, error) {
	run, err := s.store.DispatchQueuedRun()
	if err != nil {
		if errors.Is(err, store.ErrNoCapacity) {
			s.store.RecordAuditLog(domain.AuditLog{
				ActorType:    "codex",
				ActorID:      mcpActorID(ctx),
				Action:       "mcp.queue_dispatch_blocked",
				ResourceType: "queue",
				ResourceID:   "runs",
				Payload: map[string]any{
					"trigger": trigger,
					"reason":  "no_executor_capacity",
				},
			})
		} else if !errors.Is(err, store.ErrNotFound) {
			s.store.RecordAuditLog(domain.AuditLog{
				ActorType:    "codex",
				ActorID:      mcpActorID(ctx),
				Action:       "mcp.queue_dispatch_failed",
				ResourceType: "queue",
				ResourceID:   "runs",
				Payload: map[string]any{
					"trigger": trigger,
					"error":   err.Error(),
				},
			})
		}
		return domain.Run{}, err
	}
	task, err := s.store.GetTask(run.TaskID)
	if err != nil {
		s.store.RecordAuditLog(domain.AuditLog{
			ActorType:    "codex",
			ActorID:      mcpActorID(ctx),
			Action:       "mcp.queue_dispatch_failed",
			ResourceType: "run",
			ResourceID:   run.ID,
			Payload: map[string]any{
				"trigger": trigger,
				"task_id": run.TaskID,
				"error":   err.Error(),
			},
		})
		return run, err
	}
	s.store.RecordAuditLog(domain.AuditLog{
		ActorType:    "codex",
		ActorID:      mcpActorID(ctx),
		Action:       "mcp.queue_dispatch",
		ResourceType: "run",
		ResourceID:   run.ID,
		Payload: map[string]any{
			"trigger":          trigger,
			"task_id":          run.TaskID,
			"role":             run.Role,
			"executor":         run.Executor,
			"executor_node_id": run.ExecutorNodeID,
			"priority":         intFromConfig(run.Result, "queue_priority", 0),
			"attempt":          intFromConfig(run.Result, "retry_attempt", 1),
			"max_attempts":     intFromConfig(run.Result, "max_attempts", 1),
		},
	})
	s.exec.Start(ctx, task, run)
	return run, nil
}

func (s *Server) queueSnapshot() map[string]any {
	queued := []domain.Run{}
	for _, run := range s.store.ListAllRuns() {
		if run.Status == "queued" {
			queued = append(queued, run)
		}
	}
	return map[string]any{
		"queued_runs": queued,
		"backpressure": map[string]any{
			"docker": scheduler.Snapshot(s.store, "docker"),
			"ssh":    scheduler.Snapshot(s.store, "ssh"),
		},
	}
}

func mcpActorID(ctx context.Context) string {
	actorID, _ := ctx.Value(actorContextKey{}).(string)
	if actorID == "" {
		return "main"
	}
	return actorID
}

func bodyArtifactID(result map[string]any) string {
	plan, ok := result["pr_publish_plan"].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := plan["body_artifact_id"].(string)
	return value
}

func decodeRaw(w http.ResponseWriter, raw json.RawMessage, out any) bool {
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "input is required")
		return false
	}
	if err := json.Unmarshal(raw, out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid tool input: "+err.Error())
		return false
	}
	return true
}

func decodeToolInput(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	return json.Unmarshal(raw, out)
}

func objectSchema(required ...string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             required,
		"additionalProperties": true,
	}
}

func toolResult(value any) map[string]any {
	text := valueToText(value)
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"structuredContent": value,
	}
}

func valueToText(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "tool result could not be encoded"
	}
	return string(data)
}

func statusName(valid bool) string {
	if valid {
		return "succeeded"
	}
	return "failed"
}

func statusForRunStatus(runStatus string) string {
	if runStatus == "blocked" {
		return "blocked"
	}
	return "succeeded"
}

func storeErrorStatus(err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, store.ErrNoCapacity) {
		return http.StatusConflict
	}
	if errors.Is(err, store.ErrConflict) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *Server) startRunErrorOutput(ctx context.Context, err error, task domain.Task, role string, executorName string) (map[string]any, string, int) {
	if errors.Is(err, store.ErrNoCapacity) {
		if executorName == "" {
			executorName = task.Envelope.Executor
		}
		priority := queuePriorityForTask(s.store, task)
		attempt := 1
		maxAttempts := retryMaxAttemptsForTask(s.store, task)
		run, queueErr := s.store.EnqueueRun(task.ID, role, executorName, priority, attempt, maxAttempts, "capacity_full")
		if queueErr != nil {
			return map[string]any{"error": queueErr.Error()}, "failed", storeErrorStatus(queueErr)
		}
		s.store.RecordAuditLog(domain.AuditLog{
			ActorType:    "codex",
			ActorID:      mcpActorID(ctx),
			Action:       "mcp.worker_enqueue",
			ResourceType: "run",
			ResourceID:   run.ID,
			Payload: map[string]any{
				"task_id":      task.ID,
				"role":         role,
				"executor":     executorName,
				"priority":     priority,
				"attempt":      attempt,
				"max_attempts": maxAttempts,
				"reason":       "capacity_full",
			},
		})
		return map[string]any{
			"queued":       true,
			"run":          run,
			"backpressure": scheduler.Snapshot(s.store, executorName),
		}, "succeeded", http.StatusAccepted
	}
	return map[string]any{"error": err.Error()}, "failed", storeErrorStatus(err)
}

func queuePriorityForTask(st store.Store, task domain.Task) int {
	for _, profile := range st.ListAgentProfiles(task.ProjectID) {
		if profile.Name == task.Envelope.AgentProfile || profile.ID == task.Envelope.AgentProfile {
			return intFromConfig(profile.Config, "queue_priority", 0)
		}
	}
	return 0
}

func retryMaxAttemptsForTask(st store.Store, task domain.Task) int {
	for _, profile := range st.ListAgentProfiles(task.ProjectID) {
		if profile.Name != task.Envelope.AgentProfile && profile.ID != task.Envelope.AgentProfile {
			continue
		}
		if value := intFromConfig(profile.Config, "retry_max_attempts", 0); value > 0 {
			return value
		}
		if retry, ok := profile.Config["retry"].(map[string]any); ok {
			if value := intFromConfig(retry, "max_attempts", 0); value > 0 {
				return value
			}
		}
	}
	return 1
}

func intFromConfig(values map[string]any, key string, fallback int) int {
	if values == nil {
		return fallback
	}
	value, ok := values[key]
	if !ok {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, err := typed.Int64()
		if err == nil {
			return int(parsed)
		}
	case string:
		parsed, err := strconv.Atoi(typed)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func mcpSessionID(r *http.Request) string {
	if value := strings.TrimSpace(r.Header.Get("MCP-Session-Id")); validMCPSessionID(value) {
		return value
	}
	return domain.NewID("mcp_session")
}

func parseLastEventID(value string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func validMCPSessionID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeSSE(w http.ResponseWriter, id int64, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	if id > 0 {
		_, _ = w.Write([]byte("id: "))
		_, _ = w.Write([]byte(strconv.FormatInt(id, 10)))
		_, _ = w.Write([]byte("\n"))
	}
	_, _ = w.Write([]byte("event: message\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(payload)
	_, _ = w.Write([]byte("\n\n"))
}

func hashPrefix(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func acceptsMedia(header string, mediaType string) bool {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.Index(part, ";"); idx >= 0 {
			part = strings.TrimSpace(part[:idx])
		}
		if part == mediaType || part == "*/*" {
			return true
		}
		if strings.HasSuffix(part, "/*") {
			prefix := strings.TrimSuffix(part, "/*")
			if strings.HasPrefix(mediaType, prefix+"/") {
				return true
			}
		}
	}
	return false
}

func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || localOrigin(origin) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "origin is not allowed for MCP Gateway")
	})
}

func localOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func rawToMap(raw json.RawMessage) map[string]any {
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

func valueToMap(value any) map[string]any {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{"value": value}
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return map[string]any{"value": string(data)}
	}
	return out
}
