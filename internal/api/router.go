package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/auditseal"
	authn "github.com/Chiiz0/multi-codex/internal/auth"
	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/executor"
	"github.com/Chiiz0/multi-codex/internal/gitsync"
	"github.com/Chiiz0/multi-codex/internal/observability"
	"github.com/Chiiz0/multi-codex/internal/policy"
	"github.com/Chiiz0/multi-codex/internal/retention"
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

type apiAuthContextKey struct{}

const maxArtifactContentBytes int64 = 2 * 1024 * 1024
const authSessionCookieName = "multi_codex_session"

func NewServer(cfg config.Config, st store.Store, log *slog.Logger) *Server {
	if cfg.AuthSessionTTL <= 0 {
		cfg.AuthSessionTTL = 12 * time.Hour
	}
	if cfg.AuthLoginStateTTL <= 0 {
		cfg.AuthLoginStateTTL = 10 * time.Minute
	}
	var verifier *authn.OIDCVerifier
	if strings.EqualFold(cfg.AuthMode, "oidc") {
		verifier = authn.NewOIDCVerifier(cfg.OIDCIssuer, cfg.OIDCAudience, cfg.OIDCJWKSURL)
	}
	server := &Server{cfg: cfg, store: st, exec: executor.NewManager(cfg, st), metrics: observability.NewMetrics("multi-codex-api"), oidc: verifier, log: log}
	server.startRetentionWorker()
	server.startQueueWorker()
	server.startTelemetryPushWorker()
	server.startAuditShipWorker()
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.metricsEndpoint)
	mux.HandleFunc("/", s.routeAPI)
	return s.metrics.Middleware(withCORS(s.cfg, s.withRBAC(mux)))
}

func (s *Server) routeAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}

	parts := splitPath(strings.TrimPrefix(r.URL.Path, "/api/v1/"))
	switch {
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "capabilities":
		s.authCapabilities(w, r)
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "me":
		s.authMe(w, r)
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "login":
		s.authLogin(w, r)
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "callback":
		s.authCallback(w, r)
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "session":
		s.authSession(w, r)
	case len(parts) == 2 && parts[0] == "auth" && parts[1] == "logout":
		s.authLogout(w, r)
	case len(parts) == 3 && parts[0] == "auth" && parts[1] == "backchannel" && parts[2] == "logout":
		s.authBackchannelLogout(w, r)
	case len(parts) == 1 && parts[0] == "users":
		s.users(w, r)
	case len(parts) == 1 && parts[0] == "organizations":
		s.organizations(w, r)
	case len(parts) == 1 && parts[0] == "projects":
		s.projects(w, r)
	case len(parts) == 1 && parts[0] == "queue":
		s.queue(w, r)
	case len(parts) == 2 && parts[0] == "queue" && parts[1] == "dispatch":
		s.queueDispatch(w, r)
	case len(parts) == 2 && parts[0] == "projects":
		s.project(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "members":
		s.projectMembers(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "repositories":
		s.repositories(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "agent-profiles":
		s.agentProfiles(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "projects" && parts[2] == "tasks":
		s.tasks(w, r, parts[1])
	case len(parts) == 2 && parts[0] == "tasks":
		s.task(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "validate":
		s.validateTask(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "start":
		s.startTask(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "workflow":
		s.taskWorkflow(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "tasks" && parts[2] == "workflow":
		s.workflowAction(w, r, parts[1], parts[3])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "runs":
		s.taskRuns(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "scope-check":
		s.scopeCheck(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "tasks" && parts[2] == "approvals":
		s.taskApprovals(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "runs":
		s.allRuns(w, r)
	case len(parts) == 2 && parts[0] == "runs":
		s.run(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "events":
		s.runEvents(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "artifacts":
		s.runArtifacts(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "artifacts" && parts[2] == "content":
		s.artifactContent(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "runs" && parts[2] == "events" && parts[3] == "stream":
		s.runEventStream(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "runs" && parts[2] == "finish":
		s.finishRun(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "executor-nodes":
		s.executorNodes(w, r)
	case len(parts) == 3 && parts[0] == "executor-nodes" && parts[2] == "verify-host-key":
		s.verifyExecutorNodeHostKey(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "skills":
		s.skills(w, r)
	case len(parts) == 3 && parts[0] == "skills" && parts[2] == "versions":
		s.skillVersions(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "approvals":
		s.approvals(w, r)
	case len(parts) == 3 && parts[0] == "approvals" && parts[2] == "decision":
		s.decideApproval(w, r, parts[1])
	case len(parts) == 1 && parts[0] == "tool-calls":
		s.toolCalls(w, r)
	case len(parts) == 1 && parts[0] == "audit-logs":
		writeJSON(w, http.StatusOK, s.filterAuditLogsForAuth(r, s.store.ListAuditLogs()))
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "multi-codex-api"})
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) startRetentionWorker() {
	if !s.cfg.RetentionEnabled {
		return
	}
	if s.cfg.RetentionInterval <= 0 || s.cfg.RetentionMaxAge <= 0 {
		s.log.Error("retention worker disabled by invalid config", "interval", s.cfg.RetentionInterval, "max_age", s.cfg.RetentionMaxAge)
		return
	}
	go func() {
		ticker := time.NewTicker(s.cfg.RetentionInterval)
		defer ticker.Stop()
		s.log.Info("retention worker started", "interval", s.cfg.RetentionInterval, "max_age", s.cfg.RetentionMaxAge, "dry_run", s.cfg.RetentionDryRun)
		for range ticker.C {
			s.runRetentionCleanup("scheduled")
		}
	}()
}

func (s *Server) runRetentionCleanup(trigger string) {
	filesystemResult := retention.Cleanup(retention.CleanupOptions{
		Roots:  []string{s.cfg.RunRoot, s.cfg.ArtifactRoot, s.cfg.WorktreeRoot},
		MaxAge: s.cfg.RetentionMaxAge,
		DryRun: s.cfg.RetentionDryRun,
	})
	now := time.Now().UTC()
	sessionCutoff := now.Add(-s.cfg.RetentionMaxAge)
	sessionResult, sessionErr := s.store.CleanupMCPSessions(sessionCutoff, s.cfg.RetentionDryRun)
	revocationResult, revocationErr := s.store.CleanupAuthTokenRevocations(now, s.cfg.RetentionDryRun)
	authSessionResult, authSessionErr := s.store.CleanupAuthSessions(now, s.cfg.RetentionDryRun)
	loginStateResult, loginStateErr := s.store.CleanupAuthLoginStates(now, s.cfg.RetentionDryRun)
	payload := map[string]any{
		"trigger":                trigger,
		"filesystem":             filesystemResult,
		"mcp_sessions":           sessionResult,
		"auth_token_revocations": revocationResult,
		"auth_sessions":          authSessionResult,
		"auth_login_states":      loginStateResult,
	}
	if sessionErr != nil {
		payload["mcp_session_error"] = sessionErr.Error()
	}
	if revocationErr != nil {
		payload["auth_token_revocation_error"] = revocationErr.Error()
	}
	if authSessionErr != nil {
		payload["auth_session_error"] = authSessionErr.Error()
	}
	if loginStateErr != nil {
		payload["auth_login_state_error"] = loginStateErr.Error()
	}
	if len(filesystemResult.Errors) > 0 || sessionErr != nil || revocationErr != nil || authSessionErr != nil || loginStateErr != nil {
		s.log.Error("retention cleanup completed with errors", "filesystem_errors", filesystemResult.Errors, "mcp_session_error", sessionErr, "auth_token_revocation_error", revocationErr, "auth_session_error", authSessionErr, "auth_login_state_error", loginStateErr)
		s.audit("system", "api", "api.retention_cleanup_failed", "retention", "filesystem", payload)
		return
	}
	s.log.Info("retention cleanup completed", "scanned", filesystemResult.Scanned, "deleted", filesystemResult.Deleted, "mcp_sessions_deleted", sessionResult.DeletedSessions, "auth_token_revocations_deleted", revocationResult.Deleted, "auth_sessions_deleted", authSessionResult.Deleted, "auth_login_states_deleted", loginStateResult.Deleted, "dry_run", filesystemResult.DryRun)
	s.audit("system", "api", "api.retention_cleanup", "retention", "filesystem", payload)
}

func (s *Server) startAuditShipWorker() {
	if !s.cfg.AuditShipEnabled {
		return
	}
	if s.cfg.AuditShipInterval <= 0 {
		s.log.Error("audit ship worker disabled by invalid interval", "interval", s.cfg.AuditShipInterval)
		s.audit("system", "api", "api.audit_ship_worker_disabled", "audit_ship", "scheduler", map[string]any{
			"reason":   "invalid_interval",
			"interval": s.cfg.AuditShipInterval.String(),
		})
		return
	}
	if strings.TrimSpace(s.cfg.AuditSealRoot) == "" || strings.TrimSpace(s.cfg.AuditShipTarget) == "" {
		s.log.Error("audit ship worker disabled by missing config", "seal_root_set", strings.TrimSpace(s.cfg.AuditSealRoot) != "", "target_set", strings.TrimSpace(s.cfg.AuditShipTarget) != "")
		s.audit("system", "api", "api.audit_ship_worker_disabled", "audit_ship", "scheduler", map[string]any{
			"reason":        "missing_config",
			"seal_root_set": strings.TrimSpace(s.cfg.AuditSealRoot) != "",
			"target_config": auditShipTargetDescriptor(s.cfg.AuditShipTarget),
		})
		return
	}
	go func() {
		ticker := time.NewTicker(s.cfg.AuditShipInterval)
		defer ticker.Stop()
		s.log.Info("audit ship worker started", "interval", s.cfg.AuditShipInterval, "seal_root", s.cfg.AuditSealRoot, "target", auditShipTargetDescriptor(s.cfg.AuditShipTarget))
		for range ticker.C {
			s.runAuditShip("scheduled")
		}
	}()
}

func (s *Server) runAuditShip(trigger string) {
	started := time.Now().UTC()
	payload := map[string]any{
		"trigger": trigger,
		"target":  auditShipTargetDescriptor(s.cfg.AuditShipTarget),
	}
	if strings.TrimSpace(s.cfg.AuditSealRoot) == "" || strings.TrimSpace(s.cfg.AuditShipTarget) == "" {
		payload["error"] = "audit seal root and ship target are required"
		s.log.Error("audit ship skipped by missing config", "trigger", trigger)
		s.audit("system", "api", "api.audit_ship_failed", "audit_ship", "scheduler", payload)
		return
	}
	entries := s.store.ListAuditLogsForSeal()
	verification := store.VerifyAuditHashChainWithOptions(entries, store.AuditHashVerificationOptions{
		AllowLegacyHashMismatch: s.cfg.AuditShipAllowLegacyHashMismatch,
	})
	payload["verification"] = verification
	if !verification.Valid {
		payload["error"] = "audit hash-chain verification failed"
		s.log.Error("audit ship blocked by invalid audit hash chain", "errors", verification.Errors, "warnings", verification.Warnings)
		s.audit("system", "api", "api.audit_ship_failed", "audit_ship", "scheduler", payload)
		return
	}
	output := filepath.Join(s.cfg.AuditSealRoot, "audit-seal-"+started.Format("20060102T150405.000000000Z"))
	payload["output"] = output
	manifest, err := auditseal.Write(output, entries, verification)
	if err != nil {
		payload["error"] = err.Error()
		s.log.Error("audit seal write failed", "output", output, "error", err)
		s.audit("system", "api", "api.audit_ship_failed", "audit_ship", "scheduler", payload)
		return
	}
	payload["manifest"] = auditSealManifestSummary(manifest)
	receipt, err := auditseal.Ship(output, s.cfg.AuditShipTarget)
	if err != nil {
		payload["error"] = err.Error()
		s.log.Error("audit seal ship failed", "output", output, "error", err)
		s.audit("system", "api", "api.audit_ship_failed", "audit_ship", "scheduler", payload)
		return
	}
	payload["receipt"] = auditShipReceiptSummary(receipt)
	payload["duration_ms"] = time.Since(started).Milliseconds()
	s.log.Info("audit seal shipped", "output", output, "entry_count", manifest["entry_count"], "duration_ms", payload["duration_ms"])
	s.audit("system", "api", "api.audit_ship", "audit_ship", "scheduler", payload)
}

func auditShipTargetDescriptor(target string) map[string]any {
	target = strings.TrimSpace(target)
	if target == "" {
		return map[string]any{"configured": false}
	}
	if strings.HasPrefix(target, "file://") {
		return map[string]any{"configured": true, "scheme": "file", "path": filepath.Clean(strings.TrimPrefix(target, "file://"))}
	}
	parsed, err := url.Parse(target)
	if err == nil && parsed.Scheme != "" {
		return map[string]any{
			"configured": true,
			"scheme":     parsed.Scheme,
			"host":       parsed.Host,
			"path":       parsed.EscapedPath(),
		}
	}
	return map[string]any{"configured": true, "scheme": "file", "path": filepath.Clean(target)}
}

func auditSealManifestSummary(manifest map[string]any) map[string]any {
	return pickAuditSealFields(manifest, []string{"bundle_format", "entry_count", "audit_sha256", "manifest_sha256", "first_audit_id", "last_audit_id", "output", "immutable_target"})
}

func auditShipReceiptSummary(receipt map[string]any) map[string]any {
	return pickAuditSealFields(receipt, []string{"status", "bundle_format", "entry_count", "audit_sha256", "manifest_sha256", "destination", "remote_status", "immutable_target"})
}

func pickAuditSealFields(values map[string]any, keys []string) map[string]any {
	picked := map[string]any{}
	for _, key := range keys {
		if value, ok := values[key]; ok {
			picked[key] = value
		}
	}
	return picked
}

func (s *Server) startQueueWorker() {
	if !s.cfg.QueueEnabled {
		return
	}
	if s.cfg.QueueDispatchInterval <= 0 {
		s.log.Error("queue worker disabled by invalid interval", "interval", s.cfg.QueueDispatchInterval)
		return
	}
	go func() {
		ticker := time.NewTicker(s.cfg.QueueDispatchInterval)
		defer ticker.Stop()
		s.log.Info("queue worker started", "interval", s.cfg.QueueDispatchInterval)
		for range ticker.C {
			s.runQueueDispatch("scheduled")
		}
	}()
}

func (s *Server) runQueueDispatch(trigger string) {
	for {
		_, err := s.dispatchOneQueuedRun(trigger)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrNoCapacity) {
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) dispatchOneQueuedRun(trigger string) (domain.Run, error) {
	run, err := s.store.DispatchQueuedRun()
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrNoCapacity) {
			s.log.Error("queue dispatch failed", "error", err)
			s.audit("system", "api", "api.queue_dispatch_failed", "queue", "runs", map[string]any{"trigger": trigger, "error": err.Error()})
		}
		return domain.Run{}, err
	}
	task, err := s.store.GetTask(run.TaskID)
	if err != nil {
		s.log.Error("queue dispatch task lookup failed", "run_id", run.ID, "task_id", run.TaskID, "error", err)
		s.audit("system", "api", "api.queue_dispatch_failed", "run", run.ID, map[string]any{"trigger": trigger, "task_id": run.TaskID, "error": err.Error()})
		return run, err
	}
	s.audit("system", "api", "api.queue_dispatch", "run", run.ID, map[string]any{
		"trigger":          trigger,
		"task_id":          run.TaskID,
		"role":             run.Role,
		"executor":         run.Executor,
		"executor_node_id": run.ExecutorNodeID,
		"priority":         intFromMap(run.Result, "queue_priority", 0),
		"attempt":          intFromMap(run.Result, "retry_attempt", 1),
		"max_attempts":     intFromMap(run.Result, "max_attempts", 1),
	})
	s.exec.Start(context.Background(), task, run)
	return run, nil
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.queueSnapshotForRequest(r))
}

func (s *Server) queueDispatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if strings.EqualFold(s.cfg.AuthMode, "oidc") {
		s.auditHuman(r, "api.queue_dispatch_manual_denied", "queue", "runs", map[string]any{
			"reason":   "manual_dispatch_disabled_in_multi_org_mode",
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusForbidden, "manual queue dispatch is disabled in multi-organization mode")
		return
	}
	run, err := s.dispatchOneQueuedRun("manual")
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no queued run available", "queue": s.queueSnapshotForRequest(r)})
			return
		}
		if errors.Is(err, store.ErrNoCapacity) {
			w.Header().Set("Retry-After", fmt.Sprint(scheduler.DefaultRetryAfterSeconds))
			s.auditHuman(r, "api.queue_dispatch_blocked", "queue", "runs", map[string]any{"reason": "no_executor_capacity"})
			writeJSON(w, http.StatusConflict, map[string]any{"error": "no executor capacity available", "queue": s.queueSnapshotForRequest(r)})
			return
		}
		notFound(w, err)
		return
	}
	s.auditHuman(r, "api.queue_dispatch_manual", "run", run.ID, map[string]any{"task_id": run.TaskID, "role": run.Role, "executor": run.Executor})
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run, "queue": s.queueSnapshotForRequest(r)})
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

func (s *Server) queueSnapshotForRequest(r *http.Request) map[string]any {
	snapshot := s.queueSnapshot()
	if queued, ok := snapshot["queued_runs"].([]domain.Run); ok {
		snapshot["queued_runs"] = s.filterRunsForAuth(r, queued)
	}
	auth, err := s.requestAuth(r)
	if err != nil {
		return snapshot
	}
	if backpressure, ok := snapshot["backpressure"].(map[string]any); ok {
		for executorName, value := range backpressure {
			state, ok := value.(scheduler.Backpressure)
			if !ok {
				continue
			}
			filteredNodes := []scheduler.NodeState{}
			var available int64
			for _, nodeState := range state.Nodes {
				node, err := s.store.GetExecutorNode(nodeState.ID)
				if err != nil || !canAccessOrg(auth, node.OrgID) {
					continue
				}
				filteredNodes = append(filteredNodes, nodeState)
				available += nodeState.AvailableSlots
			}
			state.Nodes = filteredNodes
			state.AvailableSlots = available
			backpressure[executorName] = state
		}
	}
	return snapshot
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
		s.audit("system", "api", "api.telemetry_push_failed", "telemetry", "otlp", map[string]any{"trigger": trigger, "error": err.Error()})
		return
	}
	req, err := http.NewRequest(http.MethodPost, s.cfg.TelemetryPushURL, bytes.NewReader(data))
	if err != nil {
		s.audit("system", "api", "api.telemetry_push_failed", "telemetry", "otlp", map[string]any{"trigger": trigger, "error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		s.audit("system", "api", "api.telemetry_push_failed", "telemetry", "otlp", map[string]any{"trigger": trigger, "error": err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		s.audit("system", "api", "api.telemetry_push_failed", "telemetry", "otlp", map[string]any{"trigger": trigger, "status": resp.StatusCode})
	}
}

func (s *Server) metricsEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
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
		runs := s.store.ListAllRuns()
		_, _ = w.Write([]byte(observability.RunsPrometheusText("multi-codex-api", runs)))
		_, _ = w.Write([]byte(observability.OperationsPrometheusText("multi-codex-api", runs, s.store.ListAuditLogs())))
		return
	}
	snapshot := s.metrics.Snapshot()
	runs := s.store.ListAllRuns()
	snapshot["runs"] = observability.RunMetricsSnapshot(runs)
	snapshot["operations"] = observability.OperationsSnapshot(runs, s.store.ListAuditLogs())
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	auth, err := s.authContext(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, auth)
}

func (s *Server) authCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	oidcConfigured := strings.EqualFold(s.cfg.AuthMode, "oidc") &&
		s.oidc != nil &&
		strings.TrimSpace(s.cfg.OIDCClientID) != "" &&
		strings.TrimSpace(s.cfg.OIDCRedirectURL) != ""
	writeJSON(w, http.StatusOK, map[string]any{
		"auth_mode":           s.cfg.AuthMode,
		"oidc_configured":     oidcConfigured,
		"session_ttl_seconds": int64(s.cfg.AuthSessionTTL.Seconds()),
		"default_role":        s.cfg.OIDCDefaultRole,
		"local_admin_email":   localAdminEmail(s.cfg.LocalAdminEmail),
	})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	returnTo := sanitizeReturnTo(r.URL.Query().Get("return_to"), s.cfg.OIDCPostLoginRedirectURL)
	if !strings.EqualFold(s.cfg.AuthMode, "oidc") {
		http.Redirect(w, r, returnTo, http.StatusFound)
		return
	}
	if s.oidc == nil || strings.TrimSpace(s.cfg.OIDCClientID) == "" || strings.TrimSpace(s.cfg.OIDCRedirectURL) == "" {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login", "oidc", map[string]any{
			"reason":   "missing_oidc_client_config",
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusServiceUnavailable, "OIDC login is not configured")
		return
	}
	endpoints, err := authn.ResolveOIDCEndpoints(r.Context(), s.cfg.OIDCIssuer, s.cfg.OIDCAuthorizationURL, s.cfg.OIDCTokenURL)
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login", "oidc", map[string]any{
			"reason":   "endpoint_resolution_failed",
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusServiceUnavailable, "OIDC discovery failed")
		return
	}
	stateToken, err := newOpaqueToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "state generation failed")
		return
	}
	nonce, err := newOpaqueToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "nonce generation failed")
		return
	}
	codeVerifier, err := newOpaqueToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PKCE verifier generation failed")
		return
	}
	expiresAt := time.Now().UTC().Add(s.cfg.AuthLoginStateTTL)
	state, err := s.store.CreateAuthLoginState(domain.AuthLoginState{
		StateHash:    authn.TokenHash(stateToken),
		NonceHash:    authn.TokenHash(nonce),
		CodeVerifier: codeVerifier,
		ReturnTo:     returnTo,
		ExpiresAt:    expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "login state persistence failed")
		return
	}
	location, err := authn.BuildAuthorizationURL(endpoints.AuthorizationURL, s.cfg.OIDCClientID, s.cfg.OIDCRedirectURL, stateToken, nonce, codeVerifier)
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login_state", state.ID, map[string]any{
			"reason":   "authorization_url_build_failed",
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusServiceUnavailable, "OIDC authorization URL failed")
		return
	}
	s.audit("anonymous", "anonymous", "api.auth_login_start", "auth_login_state", state.ID, map[string]any{
		"state_hash_prefix": hashPrefix(state.StateHash),
		"return_to":         state.ReturnTo,
		"expires_at":        state.ExpiresAt.Format(time.RFC3339Nano),
		"authorization":     authEndpointDescriptor(endpoints.AuthorizationURL),
		"trace_id":          observability.TraceID(r.Context()),
	})
	http.Redirect(w, r, location, http.StatusFound)
}

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if !strings.EqualFold(s.cfg.AuthMode, "oidc") || s.oidc == nil {
		writeError(w, http.StatusServiceUnavailable, "OIDC login is not configured")
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login", "oidc", map[string]any{
			"reason":             "provider_error",
			"provider_error":     providerError,
			"provider_error_uri": r.URL.Query().Get("error_uri"),
			"trace_id":           observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "OIDC login failed")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	stateToken := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" || stateToken == "" {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login", "oidc", map[string]any{
			"reason":   "missing_code_or_state",
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusBadRequest, "code and state are required")
		return
	}
	stateHash := authn.TokenHash(stateToken)
	state, err := s.store.ConsumeAuthLoginState(stateHash, time.Now().UTC())
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login", "oidc", map[string]any{
			"reason":            "invalid_or_expired_state",
			"state_hash_prefix": hashPrefix(stateHash),
			"trace_id":          observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "OIDC state is invalid or expired")
		return
	}
	token, err := authn.ExchangeAuthCode(r.Context(), s.oidcAuthCodeConfig(), code, state.CodeVerifier)
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login_state", state.ID, map[string]any{
			"reason":            "token_exchange_failed",
			"state_hash_prefix": hashPrefix(state.StateHash),
			"error":             err.Error(),
			"trace_id":          observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "OIDC token exchange failed")
		return
	}
	claims, err := s.oidc.VerifyToken(r.Context(), token.IDToken)
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login_state", state.ID, map[string]any{
			"reason":            "id_token_verification_failed",
			"state_hash_prefix": hashPrefix(state.StateHash),
			"error":             err.Error(),
			"trace_id":          observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "OIDC ID token verification failed")
		return
	}
	if claims.Nonce == "" || authn.TokenHash(claims.Nonce) != state.NonceHash {
		s.audit("anonymous", "anonymous", "api.auth_login_failed", "auth_login_state", state.ID, map[string]any{
			"reason":            "nonce_mismatch",
			"state_hash_prefix": hashPrefix(state.StateHash),
			"subject":           claims.Subject,
			"trace_id":          observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "OIDC nonce mismatch")
		return
	}
	auth, err := s.authContextFromClaims(r, claims)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "OIDC identity mapping failed")
		return
	}
	session, err := s.createOIDCSession(w, r, auth, claims, "authorization_code")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session persistence failed")
		return
	}
	s.audit("human", auth.User.ID, "api.auth_login_callback", "auth_session", session.ID, map[string]any{
		"subject":                     claims.Subject,
		"issuer":                      claims.Issuer,
		"external_session_id_present": claims.SessionID != "",
		"state_hash_prefix":           hashPrefix(state.StateHash),
		"return_to":                   state.ReturnTo,
		"trace_id":                    observability.TraceID(r.Context()),
	})
	http.Redirect(w, r, sanitizeReturnTo(state.ReturnTo, s.cfg.OIDCPostLoginRedirectURL), http.StatusFound)
}

func (s *Server) authSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !strings.EqualFold(s.cfg.AuthMode, "oidc") {
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		auth, passwordHash, err := s.store.GetUserByEmail(strings.TrimSpace(req.Email))
		if err != nil || !authn.VerifyPassword(passwordHash, req.Password) {
			s.audit("anonymous", "anonymous", "api.auth_denied", "auth_session", "local", map[string]any{
				"method":   r.Method,
				"path":     r.URL.Path,
				"mode":     s.cfg.AuthMode,
				"email":    strings.TrimSpace(req.Email),
				"error":    "invalid local credentials",
				"trace_id": observability.TraceID(r.Context()),
			})
			writeError(w, http.StatusUnauthorized, "email or password is incorrect")
			return
		}
		if _, err := s.createAuthSession(w, r, auth, "local", auth.User.ID, "local"); err != nil {
			writeError(w, http.StatusInternalServerError, "session persistence failed")
			return
		}
		writeJSON(w, http.StatusOK, auth)
		return
	}
	if s.oidc == nil {
		writeError(w, http.StatusUnauthorized, "OIDC verifier is not configured")
		return
	}
	token, ok := authn.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		s.audit("anonymous", "anonymous", "api.auth_denied", "http_request", r.URL.Path, map[string]any{
			"method":   r.Method,
			"path":     r.URL.Path,
			"mode":     s.cfg.AuthMode,
			"error":    authn.ErrMissingBearer.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	tokenHash := authn.TokenHash(token)
	if s.store.IsAuthTokenRevoked(tokenHash, time.Now().UTC()) {
		s.audit("anonymous", "anonymous", "api.auth_denied", "http_request", r.URL.Path, map[string]any{
			"method":            r.Method,
			"path":              r.URL.Path,
			"mode":              s.cfg.AuthMode,
			"error":             "bearer token revoked",
			"token_hash_prefix": hashPrefix(tokenHash),
			"trace_id":          observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	auth, claims, err := s.authContextFromBearer(r, token)
	if err != nil {
		s.audit("anonymous", "anonymous", "api.auth_denied", "http_request", r.URL.Path, map[string]any{
			"method":   r.Method,
			"path":     r.URL.Path,
			"mode":     s.cfg.AuthMode,
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	_, err = s.createOIDCSession(w, r, auth, claims, "bearer_exchange")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session persistence failed")
		return
	}
	writeJSON(w, http.StatusOK, auth)
}

func (s *Server) authBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !strings.EqualFold(s.cfg.AuthMode, "oidc") || s.oidc == nil {
		writeError(w, http.StatusServiceUnavailable, "OIDC logout is not configured")
		return
	}
	logoutToken, err := readLogoutToken(r)
	if err != nil {
		s.audit("system", "oidc", "api.auth_backchannel_logout_failed", "auth_session", "oidc", map[string]any{
			"reason":   "missing_logout_token",
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusBadRequest, "logout_token is required")
		return
	}
	claims, err := s.oidc.VerifyToken(r.Context(), logoutToken)
	if err != nil {
		s.audit("system", "oidc", "api.auth_backchannel_logout_failed", "auth_session", "oidc", map[string]any{
			"reason":   "logout_token_verification_failed",
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusUnauthorized, "logout_token verification failed")
		return
	}
	if !containsString(claims.Events, "http://schemas.openid.net/event/backchannel-logout") {
		s.audit("system", "oidc", "api.auth_backchannel_logout_failed", "auth_session", "oidc", map[string]any{
			"reason":   "missing_backchannel_event",
			"subject":  claims.Subject,
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusBadRequest, "logout_token missing back-channel logout event")
		return
	}
	revokedAt := time.Now().UTC()
	var revoked int64
	if claims.SessionID != "" {
		revoked, err = s.store.RevokeAuthSessionsByExternalSessionID("oidc", claims.SessionID, revokedAt)
	} else {
		revoked, err = s.store.RevokeAuthSessionsBySubject("oidc", claims.Subject, revokedAt)
	}
	if err != nil {
		s.audit("system", "oidc", "api.auth_backchannel_logout_failed", "auth_session", "oidc", map[string]any{
			"reason":   "session_revoke_failed",
			"subject":  claims.Subject,
			"sid":      claims.SessionID,
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusInternalServerError, "session revoke failed")
		return
	}
	s.audit("system", "oidc", "api.auth_backchannel_logout", "auth_session", "oidc", map[string]any{
		"subject":                     claims.Subject,
		"issuer":                      claims.Issuer,
		"external_session_id_present": claims.SessionID != "",
		"revoked_sessions":            revoked,
		"token_id":                    claims.TokenID,
		"trace_id":                    observability.TraceID(r.Context()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"status": "logged_out", "revoked_sessions": revoked})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	actorType := "anonymous"
	actorID := "anonymous"
	payload := map[string]any{
		"mode":     s.cfg.AuthMode,
		"trace_id": observability.TraceID(r.Context()),
	}
	if auth, err := s.authContext(r); err == nil && auth.User.ID != "" {
		actorType = "human"
		actorID = auth.User.ID
	}
	if strings.EqualFold(s.cfg.AuthMode, "oidc") && s.oidc != nil {
		if token, ok := authn.BearerToken(r.Header.Get("Authorization")); ok {
			claims, err := s.oidc.VerifyToken(r.Context(), token)
			if err != nil {
				payload["token_revoke_error"] = err.Error()
			} else {
				mapped := authn.MapIdentity(claims, authn.IdentityMapping{
					DefaultRole:       s.cfg.OIDCDefaultRole,
					DefaultOrgID:      s.cfg.OIDCDefaultOrgID,
					GroupRoleMappings: s.cfg.OIDCGroupRoleMap,
					GroupOrgMappings:  s.cfg.OIDCGroupOrgMap,
				})
				if auth, err := s.store.UpsertExternalUser("oidc", claims.Subject, claims.Email, mapped.DisplayName, mapped.Role, mapped.OrgID); err == nil {
					actorType = "human"
					actorID = auth.User.ID
					hash := authn.TokenHash(token)
					revocation, err := s.store.RevokeAuthToken(domain.AuthTokenRevocation{
						TokenHash: hash,
						ActorID:   auth.User.ID,
						Subject:   claims.Subject,
						Reason:    "logout",
						ExpiresAt: claims.ExpiresAt,
					})
					if err != nil {
						payload["token_revoke_error"] = err.Error()
					} else {
						payload["token_revoked"] = true
						payload["token_hash_prefix"] = hashPrefix(hash)
						payload["token_expires_at"] = revocation.ExpiresAt.Format(time.RFC3339Nano)
					}
				} else {
					payload["token_revoke_error"] = err.Error()
				}
			}
		}
	}
	if cookie, err := r.Cookie(authSessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		sessionHash := authn.TokenHash(cookie.Value)
		if err := s.store.RevokeAuthSession(sessionHash, time.Now().UTC()); err != nil {
			payload["session_revoke_error"] = err.Error()
		} else {
			payload["session_revoked"] = true
			payload["session_hash_prefix"] = hashPrefix(sessionHash)
		}
	}
	http.SetCookie(w, s.clearAuthSessionCookie())
	s.audit(actorType, actorID, "api.auth_logout", "auth_session", actorID, payload)
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out", "mode": s.cfg.AuthMode})
}

func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	auth, err := s.requestAuth(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		users := s.store.ListUsers(auth.Membership.OrgID)
		memberships := s.store.ListMemberships(auth.Membership.OrgID)
		projectMemberships := []domain.ProjectMembership{}
		for _, user := range users {
			projectMemberships = append(projectMemberships, s.store.ListProjectMembershipsForUser(user.ID)...)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"users":               users,
			"memberships":         memberships,
			"project_memberships": projectMemberships,
		})
	case http.MethodPost:
		var req struct {
			Email            string `json:"email"`
			DisplayName      string `json:"display_name"`
			Role             string `json:"role"`
			Password         string `json:"password"`
			ExternalProvider string `json:"external_provider"`
			ExternalSubject  string `json:"external_subject"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Email == "" {
			writeError(w, http.StatusBadRequest, "email is required")
			return
		}
		created, err := s.store.UpsertUser(domain.User{
			Email:            req.Email,
			DisplayName:      req.DisplayName,
			ExternalProvider: req.ExternalProvider,
			ExternalSubject:  req.ExternalSubject,
		}, auth.Membership.OrgID, req.Role)
		if err != nil {
			if errors.Is(err, store.ErrInvalidID) {
				writeError(w, http.StatusBadRequest, "org_id must be a UUID")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		passwordUpdated := strings.TrimSpace(req.Password) != ""
		if passwordUpdated {
			passwordHash, err := authn.HashPassword(req.Password)
			if err != nil {
				if errors.Is(err, authn.ErrPasswordTooShort) {
					writeError(w, http.StatusBadRequest, err.Error())
					return
				}
				writeError(w, http.StatusInternalServerError, "password hashing failed")
				return
			}
			if err := s.store.SetUserPassword(created.User.ID, passwordHash); err != nil {
				if errors.Is(err, store.ErrInvalidID) || errors.Is(err, store.ErrNotFound) {
					writeError(w, http.StatusBadRequest, "user_id is invalid")
					return
				}
				writeError(w, http.StatusInternalServerError, "password update failed")
				return
			}
		}
		s.auditHuman(r, "api.user_upsert", "user", created.User.ID, map[string]any{"email": created.User.Email, "role": created.Membership.Role, "password_updated": passwordUpdated})
		writeJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) organizations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListOrganizations())
	case http.MethodPost:
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == "" || req.Slug == "" {
			writeError(w, http.StatusBadRequest, "name and slug are required")
			return
		}
		org, err := s.store.CreateOrganization(domain.Organization{Name: req.Name, Slug: req.Slug})
		if err != nil {
			notFound(w, err)
			return
		}
		s.auditHuman(r, "api.organization_create", "organization", org.ID, map[string]any{"slug": org.Slug})
		writeJSON(w, http.StatusCreated, org)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.filterProjectsForAuth(r, s.store.ListProjects()))
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Slug        string `json:"slug"`
			Description string `json:"description"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == "" || req.Slug == "" {
			writeError(w, http.StatusBadRequest, "name and slug are required")
			return
		}
		auth, err := s.requestAuth(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !canAdminOrg(auth, auth.Membership.OrgID) {
			s.denyResource(w, r, auth, "api.project_create", "organization", auth.Membership.OrgID, auth.Membership.OrgID)
			return
		}
		project := s.store.CreateProject(domain.Project{OrgID: auth.Membership.OrgID, Name: req.Name, Slug: req.Slug, Description: req.Description})
		s.auditHuman(r, "api.project_create", "project", project.ID, map[string]any{"slug": project.Slug})
		writeJSON(w, http.StatusCreated, project)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) project(w http.ResponseWriter, r *http.Request, projectID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	project, ok := s.authorizeProject(w, r, projectID, "api.project_read")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, project)
}

func (s *Server) projectMembers(w http.ResponseWriter, r *http.Request, projectID string) {
	if _, ok := s.authorizeProject(w, r, projectID, "api.project_members_access"); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListProjectMemberships(projectID))
	case http.MethodPost:
		var req struct {
			UserID string `json:"user_id"`
			Role   string `json:"role"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.UserID == "" {
			writeError(w, http.StatusBadRequest, "user_id is required")
			return
		}
		membership, err := s.store.UpsertProjectMembership(domain.ProjectMembership{
			ProjectID: projectID,
			UserID:    req.UserID,
			Role:      req.Role,
		})
		if err != nil {
			if errors.Is(err, store.ErrInvalidID) {
				writeError(w, http.StatusBadRequest, "project_id and user_id must be UUIDs")
				return
			}
			notFound(w, err)
			return
		}
		s.auditHuman(r, "api.project_member_upsert", "project", projectID, map[string]any{"user_id": req.UserID, "role": membership.Role})
		writeJSON(w, http.StatusCreated, membership)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) repositories(w http.ResponseWriter, r *http.Request, projectID string) {
	if _, ok := s.authorizeProject(w, r, projectID, "api.repositories_access"); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListRepositories(projectID))
	case http.MethodPost:
		var req domain.Repository
		if !decodeJSON(w, r, &req) {
			return
		}
		req.ProjectID = projectID
		if req.Name == "" || req.Provider == "" || req.RemoteURL == "" {
			writeError(w, http.StatusBadRequest, "name, provider, and remote_url are required")
			return
		}
		repo := s.store.CreateRepository(req)
		s.auditHuman(r, "api.repository_create", "repository", repo.ID, map[string]any{"project_id": projectID, "provider": repo.Provider})
		writeJSON(w, http.StatusCreated, repo)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) skills(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.filterSkillsForAuth(r, s.store.ListSkills()))
	case http.MethodPost:
		var req struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Role        string `json:"role"`
			Version     string `json:"version"`
			ContentHash string `json:"content_hash"`
			Path        string `json:"path"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Name == "" || req.Role == "" {
			writeError(w, http.StatusBadRequest, "name and role are required")
			return
		}
		auth, err := s.requestAuth(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		skill, err := s.store.CreateSkill(domain.Skill{
			OrgID:       auth.Membership.OrgID,
			Name:        req.Name,
			Description: req.Description,
			Role:        req.Role,
			Enabled:     true,
		}, domain.SkillVersion{
			Version:     req.Version,
			ContentHash: req.ContentHash,
			Path:        req.Path,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditHuman(r, "api.skill_register", "skill", skill.ID, map[string]any{"name": skill.Name, "role": skill.Role, "version": skill.LatestVersion})
		writeJSON(w, http.StatusCreated, skill)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) skillVersions(w http.ResponseWriter, r *http.Request, skillID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.authorizeSkill(w, r, skillID, "api.skill_versions_read"); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.ListSkillVersions(skillID))
}

func (s *Server) agentProfiles(w http.ResponseWriter, r *http.Request, projectID string) {
	if _, ok := s.authorizeProject(w, r, projectID, "api.agent_profiles_access"); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListAgentProfiles(projectID))
	case http.MethodPost:
		var profile domain.AgentProfile
		if !decodeJSON(w, r, &profile) {
			return
		}
		profile.ProjectID = projectID
		if profile.Name == "" || profile.Role == "" || profile.Model == "" || profile.Executor == "" {
			writeError(w, http.StatusBadRequest, "name, role, model, and executor are required")
			return
		}
		created, err := s.store.CreateAgentProfile(profile)
		if err != nil {
			if errors.Is(err, store.ErrInvalidID) {
				writeError(w, http.StatusBadRequest, "project_id must be a UUID")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditHuman(r, "api.agent_profile_create", "agent_profile", created.ID, map[string]any{"project_id": projectID, "role": created.Role, "executor": created.Executor})
		writeJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) executorNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.filterExecutorNodesForAuth(r, s.store.ListExecutorNodes()))
	case http.MethodPost:
		var node domain.ExecutorNode
		if !decodeJSON(w, r, &node) {
			return
		}
		if node.Kind == "" || node.Name == "" {
			writeError(w, http.StatusBadRequest, "kind and name are required")
			return
		}
		auth, err := s.requestAuth(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		node.OrgID = auth.Membership.OrgID
		created, err := s.store.RegisterExecutorNode(node)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditHuman(r, "api.executor_node_register", "executor_node", created.ID, map[string]any{"kind": created.Kind, "status": created.Status})
		writeJSON(w, http.StatusCreated, created)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) verifyExecutorNodeHostKey(w http.ResponseWriter, r *http.Request, nodeID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.authorizeExecutorNode(w, r, nodeID, "api.executor_node_host_key_verify"); !ok {
		return
	}
	var req struct {
		ObservedFingerprint string `json:"observed_fingerprint"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ObservedFingerprint == "" {
		writeError(w, http.StatusBadRequest, "observed_fingerprint is required")
		return
	}
	node, err := s.store.VerifyExecutorNodeHostKey(nodeID, req.ObservedFingerprint)
	if err != nil {
		notFound(w, err)
		return
	}
	action := "api.executor_node_host_key_verify"
	if !node.HostKeyVerified {
		action = "api.executor_node_host_key_reject"
	}
	s.auditHuman(r, action, "executor_node", node.ID, map[string]any{
		"observed_fingerprint": node.ObservedHostKeyFingerprint,
		"expected_fingerprint": node.HostKeyFingerprint,
		"verified":             node.HostKeyVerified,
		"status":               node.Status,
	})
	if !node.HostKeyVerified {
		writeJSON(w, http.StatusConflict, node)
		return
	}
	writeJSON(w, http.StatusOK, node)
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request, projectID string) {
	if _, ok := s.authorizeProject(w, r, projectID, "api.tasks_access"); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListTasks(projectID))
	case http.MethodPost:
		var req struct {
			Envelope domain.TaskEnvelope `json:"envelope"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		envelope := normalizeEnvelope(projectID, req.Envelope)
		validation := policy.ValidateTaskWithResources(s.store, envelope)
		if !validation.Valid {
			s.auditHuman(r, "api.task_create_denied", "task", envelope.TaskID, map[string]any{"errors": validation.Errors})
			writeJSON(w, http.StatusBadRequest, map[string]any{"validation": validation})
			return
		}
		task := s.store.CreateTask(envelope)
		s.auditHuman(r, "api.task_create", "task", task.ID, map[string]any{"task_key": task.TaskKey})
		writeJSON(w, http.StatusCreated, map[string]any{"task": task, "validation": validation})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) task(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.task_read")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) validateTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.task_validate")
	if !ok {
		return
	}
	validation := policy.ValidateTaskWithResources(s.store, task.Envelope)
	if validation.Valid {
		_, _ = s.store.UpdateTaskStatus(taskID, "validated")
		s.auditHuman(r, "api.task_validate", "task", taskID, map[string]any{"valid": true})
	} else {
		s.auditHuman(r, "api.task_validate_denied", "task", taskID, map[string]any{"valid": false, "errors": validation.Errors})
	}
	writeJSON(w, http.StatusOK, validation)
}

func (s *Server) startTask(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.worker_spawn")
	if !ok {
		return
	}
	validation := policy.ValidateTaskWithResources(s.store, task.Envelope)
	if !validation.Valid {
		s.auditHuman(r, "api.worker_spawn_denied", "task", taskID, map[string]any{"errors": validation.Errors})
		writeJSON(w, http.StatusConflict, map[string]any{"validation": validation})
		return
	}
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, "worker_spawn"); !ok {
		s.auditHuman(r, "api.worker_spawn_blocked", "task", taskID, map[string]any{"reasons": reasons, "next_actions": state.NextActions})
		writeJSON(w, http.StatusConflict, map[string]any{"workflow": state, "blocked_reasons": reasons})
		return
	}
	run, err := s.store.StartRun(taskID, task.Envelope.Role, task.Envelope.Executor)
	if err != nil {
		s.startRunError(w, r, err, task, task.Envelope.Role)
		return
	}
	s.auditHuman(r, "api.worker_spawn", "run", run.ID, map[string]any{"task_id": taskID, "role": run.Role, "executor": run.Executor})
	s.exec.Start(context.Background(), task, run)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run, "validation": validation})
}

func (s *Server) taskWorkflow(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.task_workflow_read")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, workflow.BuildState(s.store, task))
}

func (s *Server) workflowAction(w http.ResponseWriter, r *http.Request, taskID string, action string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.workflow_action")
	if !ok {
		return
	}
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, action); !ok {
		s.auditHuman(r, "api.workflow_action_blocked", "task", taskID, map[string]any{"action": action, "reasons": reasons, "next_actions": state.NextActions})
		writeJSON(w, http.StatusConflict, map[string]any{"workflow": state, "blocked_reasons": reasons})
		return
	}

	switch action {
	case "test_run_required":
		s.startRoleRun(w, r, task, "test")
	case "audit_run":
		s.startRoleRun(w, r, task, "audit")
	case "approval_request":
		approval, err := s.store.CreateApproval(domain.Approval{
			TaskID:       taskID,
			ApprovalType: "pr_prepare",
			Status:       "pending",
			Reason:       "Human approval is required before Git Sync prepares PR material.",
		})
		if err != nil {
			notFound(w, err)
			return
		}
		s.auditHuman(r, "api.approval_request", "approval", approval.ID, map[string]any{"task_id": taskID, "approval_type": approval.ApprovalType})
		writeJSON(w, http.StatusCreated, approval)
	case "approval_request_pr_publish":
		approval, err := s.store.CreateApproval(domain.Approval{
			TaskID:       taskID,
			ApprovalType: "pr_publish",
			Status:       "pending",
			Reason:       "Human approval is required before Git Sync publishes a PR operation.",
		})
		if err != nil {
			notFound(w, err)
			return
		}
		s.auditHuman(r, "api.approval_request", "approval", approval.ID, map[string]any{"task_id": taskID, "approval_type": approval.ApprovalType})
		writeJSON(w, http.StatusCreated, approval)
	case "git_prepare_pr":
		s.preparePR(w, r, task)
	case "git_publish_pr":
		s.publishPR(w, r, task, state)
	default:
		writeError(w, http.StatusBadRequest, "unsupported workflow action")
	}
}

func (s *Server) startRoleRun(w http.ResponseWriter, r *http.Request, task domain.Task, role string) {
	run, err := s.store.StartRun(task.ID, role, task.Envelope.Executor)
	if err != nil {
		s.startRunError(w, r, err, task, role)
		return
	}
	_, _ = s.store.AddEvent(run.ID, "info", "workflow_gate", "Workflow gate started role run", map[string]any{"role": role})
	s.auditHuman(r, "api.workflow_run_start", "run", run.ID, map[string]any{"task_id": task.ID, "role": role})
	s.exec.Start(context.Background(), task, run)
	writeJSON(w, http.StatusAccepted, map[string]any{"run": run})
}

func (s *Server) taskRuns(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, ok := s.authorizeTask(w, r, taskID, "api.task_runs_read"); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.ListRuns(taskID))
}

func (s *Server) allRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.filterRunsForAuth(r, s.store.ListAllRuns()))
}

func (s *Server) scopeCheck(w http.ResponseWriter, r *http.Request, taskID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	task, ok := s.authorizeTask(w, r, taskID, "api.scope_check")
	if !ok {
		return
	}
	var req struct {
		ChangedFiles []string `json:"changed_files"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	result := policy.CheckScope(req.ChangedFiles, task.Envelope.AllowedPaths, task.Envelope.ForbiddenPaths)
	record, err := s.store.RecordScopeCheck(taskID, "", task.Envelope.BaseBranch, result)
	if err != nil {
		notFound(w, err)
		return
	}
	if result.Status == "blocked" {
		_, _ = s.store.UpdateTaskStatus(taskID, "blocked")
	}
	s.auditHuman(r, "api.scope_check", "task", taskID, map[string]any{"status": result.Status, "changed_files": result.ChangedFiles, "violations": result.Violations})
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         result.Status,
		"changed_files":  result.ChangedFiles,
		"violations":     result.Violations,
		"scope_check_id": record.ID,
	})
}

func (s *Server) taskApprovals(w http.ResponseWriter, r *http.Request, taskID string) {
	if _, ok := s.authorizeTask(w, r, taskID, "api.task_approvals_access"); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.ListApprovals(taskID))
	case http.MethodPost:
		var req struct {
			ApprovalType string `json:"approval_type"`
			Reason       string `json:"reason"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.ApprovalType == "" {
			req.ApprovalType = "pr_prepare"
		}
		approval, err := s.store.CreateApproval(domain.Approval{
			TaskID:       taskID,
			ApprovalType: req.ApprovalType,
			Status:       "pending",
			Reason:       req.Reason,
		})
		if err != nil {
			notFound(w, err)
			return
		}
		s.auditHuman(r, "api.approval_request", "approval", approval.ID, map[string]any{"task_id": taskID, "approval_type": approval.ApprovalType})
		writeJSON(w, http.StatusCreated, approval)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) approvals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.filterApprovalsForAuth(r, s.store.ListApprovals("")))
}

func (s *Server) decideApproval(w http.ResponseWriter, r *http.Request, approvalID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status != "approved" && req.Status != "rejected" {
		writeError(w, http.StatusBadRequest, "status must be approved or rejected")
		return
	}
	if _, ok := s.authorizeApproval(w, r, approvalID, "api.approval_decide"); !ok {
		return
	}
	approval, err := s.store.DecideApproval(approvalID, req.Status, "", req.Reason)
	if err != nil {
		notFound(w, err)
		return
	}
	if req.Status == "rejected" {
		_, _ = s.store.UpdateTaskStatus(approval.TaskID, "blocked")
	}
	s.auditHuman(r, "api.approval_decide", "approval", approval.ID, map[string]any{"task_id": approval.TaskID, "status": approval.Status})
	writeJSON(w, http.StatusOK, approval)
}

func (s *Server) run(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	run, _, ok := s.authorizeRun(w, r, runID, "api.run_read")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) runEvents(w http.ResponseWriter, r *http.Request, runID string) {
	switch r.Method {
	case http.MethodGet:
		if _, _, ok := s.authorizeRun(w, r, runID, "api.run_events_read"); !ok {
			return
		}
		writeJSON(w, http.StatusOK, s.store.ListEvents(runID))
	case http.MethodPost:
		if _, _, ok := s.authorizeRun(w, r, runID, "api.run_events_write"); !ok {
			return
		}
		var req struct {
			Level     string         `json:"level"`
			EventType string         `json:"event_type"`
			Message   string         `json:"message"`
			Payload   map[string]any `json:"payload"`
		}
		if !decodeJSON(w, r, &req) {
			return
		}
		if req.Level == "" {
			req.Level = "info"
		}
		if req.EventType == "" || req.Message == "" {
			writeError(w, http.StatusBadRequest, "event_type and message are required")
			return
		}
		event, err := s.store.AddEvent(runID, req.Level, req.EventType, req.Message, req.Payload)
		if err != nil {
			notFound(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, event)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) runArtifacts(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if _, _, ok := s.authorizeRun(w, r, runID, "api.run_artifacts_read"); !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.store.ListArtifacts(runID))
}

func (s *Server) artifactContent(w http.ResponseWriter, r *http.Request, artifactID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	artifact, ok := s.authorizeArtifact(w, r, artifactID, "api.artifact_read")
	if !ok {
		return
	}
	content, truncated, err := readArtifactContent(artifact)
	if err != nil {
		s.auditHuman(r, "api.artifact_read_failed", "artifact", artifact.ID, map[string]any{
			"run_id":   artifact.RunID,
			"kind":     artifact.Kind,
			"path":     artifact.Path,
			"error":    err.Error(),
			"trace_id": observability.TraceID(r.Context()),
		})
		if errors.Is(err, store.ErrNotFound) {
			notFound(w, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	contentType := artifactContentType(artifact)
	s.auditHuman(r, "api.artifact_read", "artifact", artifact.ID, map[string]any{
		"run_id":       artifact.RunID,
		"kind":         artifact.Kind,
		"name":         artifact.Name,
		"path":         artifact.Path,
		"content_type": contentType,
		"truncated":    truncated,
		"trace_id":     observability.TraceID(r.Context()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"artifact":     artifact,
		"content":      content,
		"content_type": contentType,
		"truncated":    truncated,
		"limit_bytes":  maxArtifactContentBytes,
	})
}

func (s *Server) finishRun(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if _, _, ok := s.authorizeRun(w, r, runID, "api.worker_result"); !ok {
		return
	}
	var req struct {
		Status string         `json:"status"`
		Result map[string]any `json:"result"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Status == "" {
		writeError(w, http.StatusBadRequest, "status is required")
		return
	}
	if req.Result == nil {
		req.Result = map[string]any{}
	}
	run, err := s.store.FinishRun(runID, req.Status, req.Result)
	if err != nil {
		notFound(w, err)
		return
	}
	s.audit("worker", "local-dev", "api.worker_result", "run", runID, map[string]any{"status": req.Status})
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) toolCalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, s.store.ListToolCalls())
}

func (s *Server) preparePR(w http.ResponseWriter, r *http.Request, task domain.Task) {
	state := workflow.BuildState(s.store, task)
	if ok, reasons := workflow.GateAllows(state, "git_prepare_pr"); !ok {
		s.auditHuman(r, "api.git_prepare_pr_blocked", "task", task.ID, map[string]any{"reasons": reasons, "next_actions": state.NextActions})
		writeJSON(w, http.StatusConflict, map[string]any{"workflow": state, "blocked_reasons": reasons})
		return
	}
	run, err := s.store.StartRun(task.ID, "git_sync", task.Envelope.Executor)
	if err != nil {
		s.startRunError(w, r, err, task, "git_sync")
		return
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
	s.auditHuman(r, "api.git_prepare_pr", "run", run.ID, map[string]any{"task_id": task.ID, "artifact_id": artifact.ID, "allow_push": false, "requires_approval": "pr_publish"})
	writeJSON(w, http.StatusCreated, map[string]any{"run": run, "result": result, "artifact": artifact})
}

func (s *Server) publishPR(w http.ResponseWriter, r *http.Request, task domain.Task, state domain.WorkflowState) {
	if ok, reasons := workflow.GateAllows(state, "git_publish_pr"); !ok {
		s.auditHuman(r, "api.git_publish_pr_blocked", "task", task.ID, map[string]any{"reasons": reasons, "next_actions": state.NextActions})
		writeJSON(w, http.StatusConflict, map[string]any{"workflow": state, "blocked_reasons": reasons})
		return
	}
	preparedRun := workflow.LatestRunWithResultStatus(state.Runs, "git_sync", "prepared")
	if preparedRun == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"workflow": state, "blocked_reasons": []string{"PR body has not been prepared"}})
		return
	}
	prBody, _ := preparedRun.Result["pr_body"].(string)
	if prBody == "" {
		writeError(w, http.StatusConflict, "prepared PR body is missing")
		return
	}
	repo, err := s.store.GetRepository(task.RepositoryID)
	if err != nil {
		notFound(w, err)
		return
	}
	run, err := s.store.StartRun(task.ID, "git_sync", task.Envelope.Executor)
	if err != nil {
		s.startRunError(w, r, err, task, "git_sync")
		return
	}
	result := gitsync.PublishPR(r.Context(), gitsync.PublishRequest{
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
	s.auditHuman(r, "api.git_publish_pr", "run", run.ID, map[string]any{"task_id": task.ID, "status": result.Status, "dry_run": result.DryRun, "provider": result.Provider, "credential_provider": result.CredentialProvider, "credential_resolved": result.CredentialResolved, "auto_merge": false})
	writeJSON(w, http.StatusCreated, map[string]any{"run": finished, "result": result})
}

func (s *Server) runEventStream(w http.ResponseWriter, r *http.Request, runID string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	run, err := s.store.GetRun(runID)
	if err != nil {
		notFound(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	lastSeq := int64(0)
	events := s.store.ListEvents(runID)
	for _, event := range events {
		writeRunEventSSE(w, event)
		if event.Seq > lastSeq {
			lastSeq = event.Seq
		}
	}
	flusher.Flush()
	s.auditHuman(r, "api.run_event_stream_open", "run", runID, map[string]any{
		"task_id":        run.TaskID,
		"initial_events": len(events),
		"last_seq":       lastSeq,
	})
	defer func() {
		s.auditHuman(r, "api.run_event_stream_close", "run", runID, map[string]any{
			"task_id":  run.TaskID,
			"last_seq": lastSeq,
		})
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastHeartbeat := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case now := <-ticker.C:
			sent := 0
			for _, event := range eventsAfterSeq(s.store.ListEvents(runID), lastSeq) {
				writeRunEventSSE(w, event)
				lastSeq = event.Seq
				sent++
			}
			if now.Sub(lastHeartbeat) >= 15*time.Second {
				writeNamedSSE(w, "heartbeat", map[string]any{"event_type": "heartbeat", "created_at": now.UTC()})
				lastHeartbeat = now
				sent++
			}
			if sent > 0 {
				flusher.Flush()
			}
		}
	}
}

func eventsAfterSeq(events []domain.RunEvent, afterSeq int64) []domain.RunEvent {
	next := []domain.RunEvent{}
	for _, event := range events {
		if event.Seq > afterSeq {
			next = append(next, event)
		}
	}
	return next
}

func normalizeEnvelope(projectID string, envelope domain.TaskEnvelope) domain.TaskEnvelope {
	if envelope.TaskID == "" {
		envelope.TaskID = domain.NewID("TASK")
	}
	if envelope.ProjectID == "" {
		envelope.ProjectID = projectID
	}
	if envelope.BaseBranch == "" {
		envelope.BaseBranch = "origin/main"
	}
	if envelope.TargetBranch == "" {
		envelope.TargetBranch = "codex/" + strings.ToLower(envelope.TaskID)
	}
	if envelope.Executor == "" {
		envelope.Executor = "docker"
	}
	if envelope.Role == "" {
		envelope.Role = "feature"
	}
	if envelope.Skill == "" {
		envelope.Skill = "company-feature-worker"
	}
	if envelope.AgentProfile == "" {
		envelope.AgentProfile = "feature-worker-go-node"
	}
	return envelope
}

func renderPRBody(task domain.Task, state domain.WorkflowState) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(task.Title)
	b.WriteString("\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString("- Task: `")
	b.WriteString(task.TaskKey)
	b.WriteString("`\n")
	b.WriteString("- Project: `")
	b.WriteString(task.ProjectID)
	b.WriteString("`\n")
	b.WriteString("- Repository: `")
	b.WriteString(task.RepositoryID)
	b.WriteString("`\n")
	b.WriteString("- Target branch: `")
	b.WriteString(task.Envelope.TargetBranch)
	b.WriteString("`\n\n")

	b.WriteString("## Objective\n\n")
	b.WriteString(task.Envelope.Objective)
	b.WriteString("\n\n")

	b.WriteString("## Acceptance Criteria\n\n")
	if len(task.Envelope.AcceptanceCriteria) == 0 {
		b.WriteString("- No acceptance criteria were provided.\n")
	} else {
		for _, item := range task.Envelope.AcceptanceCriteria {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Gate Results\n\n")
	if state.LatestScopeCheck != nil {
		b.WriteString("- Scope: `")
		b.WriteString(state.LatestScopeCheck.Result.Status)
		b.WriteString("`")
		if len(state.LatestScopeCheck.Result.ChangedFiles) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(state.LatestScopeCheck.Result.ChangedFiles, ", "))
			b.WriteString(")")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- Scope: not recorded\n")
	}
	b.WriteString("- Test: `")
	b.WriteString(latestRunStatus(state.Runs, "test"))
	b.WriteString("`\n")
	b.WriteString("- Audit: `")
	b.WriteString(latestRunStatus(state.Runs, "audit"))
	b.WriteString("`\n")
	b.WriteString("- Human approval: `")
	b.WriteString(approvalStatus(state.Approvals, "pr_prepare"))
	b.WriteString("`\n\n")

	b.WriteString("## Safety Notes\n\n")
	b.WriteString("- Git Sync prepared this PR body only.\n")
	b.WriteString("- No push or merge is performed automatically.\n")
	b.WriteString("- Human review remains required before any remote operation.\n")
	return b.String()
}

func latestRunStatus(runs []domain.Run, role string) string {
	for i := len(runs) - 1; i >= 0; i-- {
		if runs[i].Role == role || (role == "git_sync" && runs[i].Role == "git-sync") {
			return runs[i].Status
		}
	}
	return "not_run"
}

func approvalStatus(approvals []domain.Approval, approvalType string) string {
	for _, approval := range approvals {
		if approval.ApprovalType == approvalType {
			return approval.Status
		}
	}
	return "not_requested"
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func withCORS(cfg config.Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		restricted := cfg.IsProduction() || len(cfg.CORSAllowedOrigins) > 0
		if origin := r.Header.Get("Origin"); origin != "" {
			if corsOriginAllowed(cfg, origin) {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			} else if restricted {
				writeError(w, http.StatusForbidden, "origin is not allowed")
				return
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
			}
		} else {
			if !restricted {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
		}
		w.Header().Set("Access-Control-Allow-Headers", "content-type, authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, OPTIONS")
		next.ServeHTTP(w, r)
	})
}

func corsOriginAllowed(cfg config.Config, origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return false
	}
	for _, allowed := range cfg.CORSAllowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == origin {
			return true
		}
		if allowed == "*" && !cfg.IsProduction() {
			return true
		}
	}
	return false
}

func (s *Server) withRBAC(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			next.ServeHTTP(w, r)
			return
		}
		required := requiredPermission(r.Method, r.URL.Path)
		if required == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth, err := s.authContext(r)
		if err != nil {
			s.audit("anonymous", "anonymous", "api.auth_denied", "http_request", r.URL.Path, map[string]any{
				"method":   r.Method,
				"path":     r.URL.Path,
				"mode":     s.cfg.AuthMode,
				"error":    err.Error(),
				"trace_id": observability.TraceID(r.Context()),
			})
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if hasPermission(auth.Permissions, required) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), apiAuthContextKey{}, auth)))
			return
		}
		s.audit("human", auth.User.ID, "api.rbac_denied", "http_request", r.URL.Path, map[string]any{
			"required_permission": required,
			"method":              r.Method,
			"path":                r.URL.Path,
			"role":                auth.Membership.Role,
			"trace_id":            observability.TraceID(r.Context()),
		})
		writeError(w, http.StatusForbidden, "permission denied")
	})
}

func requiredPermission(method string, path string) string {
	if path == "/api/v1/auth/capabilities" || path == "/api/v1/auth/login" || path == "/api/v1/auth/callback" || path == "/api/v1/auth/backchannel/logout" {
		return ""
	}
	if method == http.MethodGet {
		if strings.Contains(path, "/users") {
			return "users:read"
		}
		if strings.Contains(path, "/organizations") {
			return "organizations:read"
		}
		if strings.Contains(path, "/members") {
			return "projects:read"
		}
		if strings.Contains(path, "/audit-logs") || strings.Contains(path, "/tool-calls") {
			return "audit:read"
		}
		if strings.Contains(path, "/artifacts/") || strings.Contains(path, "/runs/") {
			return "runs:read"
		}
		if strings.Contains(path, "/queue") {
			return "runs:read"
		}
		if strings.Contains(path, "/executor-nodes") {
			return "nodes:read"
		}
		return "projects:read"
	}
	if strings.Contains(path, "/users") {
		return "users:write"
	}
	if strings.Contains(path, "/organizations") {
		return "organizations:write"
	}
	if strings.Contains(path, "/members") {
		return "projects:write"
	}
	if strings.Contains(path, "/executor-nodes") {
		return "nodes:write"
	}
	if strings.Contains(path, "/skills") {
		return "skills:write"
	}
	if strings.Contains(path, "/approvals") {
		return "approvals:write"
	}
	if strings.Contains(path, "/runs") || strings.Contains(path, "/start") || strings.Contains(path, "/workflow") {
		return "runs:write"
	}
	if strings.Contains(path, "/queue") {
		return "runs:write"
	}
	if strings.Contains(path, "/tasks") {
		return "tasks:write"
	}
	if strings.Contains(path, "/repositories") {
		return "repositories:write"
	}
	if strings.Contains(path, "/projects") {
		return "projects:write"
	}
	return ""
}

func hasPermission(permissions []string, required string) bool {
	for _, permission := range permissions {
		if permission == "*" || permission == required {
			return true
		}
	}
	return false
}

func (s *Server) requestAuth(r *http.Request) (domain.AuthContext, error) {
	if auth, ok := r.Context().Value(apiAuthContextKey{}).(domain.AuthContext); ok {
		return auth, nil
	}
	return s.authContext(r)
}

func canAccessOrg(auth domain.AuthContext, orgID string) bool {
	return orgID == "" || auth.Membership.OrgID == "" || auth.Membership.OrgID == orgID
}

func canAdminOrg(auth domain.AuthContext, orgID string) bool {
	return canAccessOrg(auth, orgID) && hasPermission(auth.Permissions, "*")
}

func canAccessProject(auth domain.AuthContext, project domain.Project) bool {
	if canAdminOrg(auth, project.OrgID) {
		return true
	}
	if !canAccessOrg(auth, project.OrgID) {
		return false
	}
	for _, membership := range auth.ProjectMemberships {
		if membership.ProjectID == project.ID {
			return true
		}
	}
	return false
}

func (s *Server) denyResource(w http.ResponseWriter, r *http.Request, auth domain.AuthContext, action string, resourceType string, resourceID string, resourceOrgID string) {
	actorID := auth.User.ID
	if actorID == "" {
		actorID = "anonymous"
	}
	s.auditWithOrg(auth.Membership.OrgID, "human", actorID, "api.authorization_denied", resourceType, resourceID, map[string]any{
		"action":          action,
		"actor_org_id":    auth.Membership.OrgID,
		"resource_org_id": resourceOrgID,
		"trace_id":        observability.TraceID(r.Context()),
	})
	writeError(w, http.StatusForbidden, "permission denied")
}

func (s *Server) authorizeProject(w http.ResponseWriter, r *http.Request, projectID string, action string) (domain.Project, bool) {
	project, err := s.store.GetProject(projectID)
	if err != nil {
		notFound(w, err)
		return domain.Project{}, false
	}
	auth, err := s.requestAuth(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return domain.Project{}, false
	}
	if !canAccessProject(auth, project) {
		s.denyResource(w, r, auth, action, "project", project.ID, project.OrgID)
		return domain.Project{}, false
	}
	return project, true
}

func (s *Server) authorizeTask(w http.ResponseWriter, r *http.Request, taskID string, action string) (domain.Task, bool) {
	task, err := s.store.GetTask(taskID)
	if err != nil {
		notFound(w, err)
		return domain.Task{}, false
	}
	if _, ok := s.authorizeProject(w, r, task.ProjectID, action); !ok {
		return domain.Task{}, false
	}
	return task, true
}

func (s *Server) authorizeRun(w http.ResponseWriter, r *http.Request, runID string, action string) (domain.Run, domain.Task, bool) {
	run, err := s.store.GetRun(runID)
	if err != nil {
		notFound(w, err)
		return domain.Run{}, domain.Task{}, false
	}
	task, ok := s.authorizeTask(w, r, run.TaskID, action)
	if !ok {
		return domain.Run{}, domain.Task{}, false
	}
	return run, task, true
}

func (s *Server) authorizeArtifact(w http.ResponseWriter, r *http.Request, artifactID string, action string) (domain.Artifact, bool) {
	artifact, err := s.store.GetArtifact(artifactID)
	if err != nil {
		s.auditHuman(r, "api.artifact_read_missing", "artifact", artifactID, map[string]any{
			"trace_id": observability.TraceID(r.Context()),
		})
		notFound(w, err)
		return domain.Artifact{}, false
	}
	if _, _, ok := s.authorizeRun(w, r, artifact.RunID, action); !ok {
		return domain.Artifact{}, false
	}
	return artifact, true
}

func (s *Server) authorizeSkill(w http.ResponseWriter, r *http.Request, skillID string, action string) (domain.Skill, bool) {
	auth, err := s.requestAuth(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return domain.Skill{}, false
	}
	for _, skill := range s.store.ListSkills() {
		if skill.ID != skillID {
			continue
		}
		if !canAccessOrg(auth, skill.OrgID) {
			s.denyResource(w, r, auth, action, "skill", skill.ID, skill.OrgID)
			return domain.Skill{}, false
		}
		return skill, true
	}
	notFound(w, store.ErrNotFound)
	return domain.Skill{}, false
}

func (s *Server) authorizeExecutorNode(w http.ResponseWriter, r *http.Request, nodeID string, action string) (domain.ExecutorNode, bool) {
	node, err := s.store.GetExecutorNode(nodeID)
	if err != nil {
		notFound(w, err)
		return domain.ExecutorNode{}, false
	}
	auth, err := s.requestAuth(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return domain.ExecutorNode{}, false
	}
	if !canAccessOrg(auth, node.OrgID) {
		s.denyResource(w, r, auth, action, "executor_node", node.ID, node.OrgID)
		return domain.ExecutorNode{}, false
	}
	return node, true
}

func (s *Server) authorizeApproval(w http.ResponseWriter, r *http.Request, approvalID string, action string) (domain.Approval, bool) {
	for _, approval := range s.store.ListApprovals("") {
		if approval.ID != approvalID {
			continue
		}
		if _, ok := s.authorizeTask(w, r, approval.TaskID, action); !ok {
			return domain.Approval{}, false
		}
		return approval, true
	}
	notFound(w, store.ErrNotFound)
	return domain.Approval{}, false
}

func (s *Server) filterProjectsForAuth(r *http.Request, projects []domain.Project) []domain.Project {
	auth, err := s.requestAuth(r)
	if err != nil {
		return nil
	}
	filtered := []domain.Project{}
	for _, project := range projects {
		if canAccessProject(auth, project) {
			filtered = append(filtered, project)
		}
	}
	return filtered
}

func (s *Server) filterSkillsForAuth(r *http.Request, skills []domain.Skill) []domain.Skill {
	auth, err := s.requestAuth(r)
	if err != nil {
		return nil
	}
	filtered := []domain.Skill{}
	for _, skill := range skills {
		if canAccessOrg(auth, skill.OrgID) {
			filtered = append(filtered, skill)
		}
	}
	return filtered
}

func (s *Server) filterExecutorNodesForAuth(r *http.Request, nodes []domain.ExecutorNode) []domain.ExecutorNode {
	auth, err := s.requestAuth(r)
	if err != nil {
		return nil
	}
	filtered := []domain.ExecutorNode{}
	for _, node := range nodes {
		if canAccessOrg(auth, node.OrgID) {
			filtered = append(filtered, node)
		}
	}
	return filtered
}

func (s *Server) filterRunsForAuth(r *http.Request, runs []domain.Run) []domain.Run {
	filtered := []domain.Run{}
	for _, run := range runs {
		task, err := s.store.GetTask(run.TaskID)
		if err != nil {
			continue
		}
		project, err := s.store.GetProject(task.ProjectID)
		if err != nil {
			continue
		}
		auth, err := s.requestAuth(r)
		if err != nil || !canAccessProject(auth, project) {
			continue
		}
		filtered = append(filtered, run)
	}
	return filtered
}

func (s *Server) filterApprovalsForAuth(r *http.Request, approvals []domain.Approval) []domain.Approval {
	filtered := []domain.Approval{}
	for _, approval := range approvals {
		task, err := s.store.GetTask(approval.TaskID)
		if err != nil {
			continue
		}
		project, err := s.store.GetProject(task.ProjectID)
		if err != nil {
			continue
		}
		auth, err := s.requestAuth(r)
		if err != nil || !canAccessProject(auth, project) {
			continue
		}
		filtered = append(filtered, approval)
	}
	return filtered
}

func (s *Server) filterAuditLogsForAuth(r *http.Request, logs []domain.AuditLog) []domain.AuditLog {
	auth, err := s.requestAuth(r)
	if err != nil {
		return nil
	}
	filtered := []domain.AuditLog{}
	for _, entry := range logs {
		if entry.OrgID == "" || canAccessOrg(auth, entry.OrgID) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func (s *Server) authContext(r *http.Request) (domain.AuthContext, error) {
	if cookie, err := r.Cookie(authSessionCookieName); err == nil && strings.TrimSpace(cookie.Value) != "" {
		auth, _, err := s.store.GetAuthSession(authn.TokenHash(cookie.Value), time.Now().UTC())
		if err == nil {
			return auth, nil
		}
	}
	if !strings.EqualFold(s.cfg.AuthMode, "oidc") {
		return domain.AuthContext{}, authn.ErrMissingBearer
	}
	if s.oidc == nil {
		return domain.AuthContext{}, fmt.Errorf("OIDC verifier is not configured")
	}
	token, ok := authn.BearerToken(r.Header.Get("Authorization"))
	if !ok {
		return domain.AuthContext{}, authn.ErrMissingBearer
	}
	tokenHash := authn.TokenHash(token)
	if s.store.IsAuthTokenRevoked(tokenHash, time.Now().UTC()) {
		return domain.AuthContext{}, fmt.Errorf("bearer token revoked")
	}
	auth, _, err := s.authContextFromBearer(r, token)
	return auth, err
}

func (s *Server) authContextFromBearer(r *http.Request, token string) (domain.AuthContext, authn.Claims, error) {
	claims, err := s.oidc.VerifyToken(r.Context(), token)
	if err != nil {
		return domain.AuthContext{}, authn.Claims{}, err
	}
	auth, err := s.authContextFromClaims(r, claims)
	return auth, claims, err
}

func (s *Server) authContextFromClaims(r *http.Request, claims authn.Claims) (domain.AuthContext, error) {
	mapped := authn.MapIdentity(claims, authn.IdentityMapping{
		DefaultRole:       s.cfg.OIDCDefaultRole,
		DefaultOrgID:      s.cfg.OIDCDefaultOrgID,
		GroupRoleMappings: s.cfg.OIDCGroupRoleMap,
		GroupOrgMappings:  s.cfg.OIDCGroupOrgMap,
	})
	auth, err := s.store.UpsertExternalUser("oidc", claims.Subject, claims.Email, mapped.DisplayName, mapped.Role, mapped.OrgID)
	if err != nil {
		return domain.AuthContext{}, err
	}
	s.audit("human", auth.User.ID, "api.auth_oidc_mapped", "user", auth.User.ID, map[string]any{
		"subject":            claims.Subject,
		"issuer":             claims.Issuer,
		"role":               auth.Membership.Role,
		"org_id":             auth.Membership.OrgID,
		"matched_role_claim": mapped.MatchedRoleClaim,
		"matched_org_claim":  mapped.MatchedOrgClaim,
		"trace_id":           observability.TraceID(r.Context()),
	})
	return auth, nil
}

func (s *Server) createOIDCSession(w http.ResponseWriter, r *http.Request, auth domain.AuthContext, claims authn.Claims, source string) (domain.AuthSession, error) {
	expiresAt := time.Now().UTC().Add(s.cfg.AuthSessionTTL)
	if !claims.ExpiresAt.IsZero() && claims.ExpiresAt.Before(expiresAt) {
		expiresAt = claims.ExpiresAt
	}
	session, err := s.createAuthSessionWithExternalID(w, r, auth, "oidc", claims.Subject, claims.SessionID, expiresAt, map[string]any{
		"subject":                     claims.Subject,
		"issuer":                      claims.Issuer,
		"source":                      source,
		"external_session_id_present": claims.SessionID != "",
	})
	return session, err
}

func (s *Server) createAuthSession(w http.ResponseWriter, r *http.Request, auth domain.AuthContext, provider string, subject string, source string) (domain.AuthSession, error) {
	return s.createAuthSessionWithExternalID(w, r, auth, provider, subject, "", time.Now().UTC().Add(s.cfg.AuthSessionTTL), map[string]any{
		"subject": subject,
		"source":  source,
	})
}

func (s *Server) createAuthSessionWithExternalID(w http.ResponseWriter, r *http.Request, auth domain.AuthContext, provider string, subject string, externalSessionID string, expiresAt time.Time, payload map[string]any) (domain.AuthSession, error) {
	sessionToken, err := newOpaqueToken()
	if err != nil {
		return domain.AuthSession{}, err
	}
	session, err := s.store.CreateAuthSessionWithExternalID(authn.TokenHash(sessionToken), auth, provider, subject, externalSessionID, expiresAt)
	if err != nil {
		return domain.AuthSession{}, err
	}
	http.SetCookie(w, s.authSessionCookie(sessionToken, expiresAt))
	if payload == nil {
		payload = map[string]any{}
	}
	payload["provider"] = provider
	payload["expires_at"] = expiresAt.Format(time.RFC3339Nano)
	payload["session_hash_prefix"] = hashPrefix(session.TokenHash)
	payload["trace_id"] = observability.TraceID(r.Context())
	s.audit("human", auth.User.ID, "api.auth_session_create", "auth_session", session.ID, payload)
	return session, nil
}

func (s *Server) oidcAuthCodeConfig() authn.AuthCodeConfig {
	return authn.AuthCodeConfig{
		Issuer:           s.cfg.OIDCIssuer,
		ClientID:         s.cfg.OIDCClientID,
		ClientSecret:     s.cfg.OIDCClientSecret,
		ClientAuthMethod: s.cfg.OIDCClientAuthMethod,
		RedirectURL:      s.cfg.OIDCRedirectURL,
		AuthorizationURL: s.cfg.OIDCAuthorizationURL,
		TokenURL:         s.cfg.OIDCTokenURL,
	}
}

func hashPrefix(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func sanitizeReturnTo(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(fallback)
	}
	if value == "" {
		return "/"
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" || !strings.HasPrefix(parsed.Path, "/") || strings.HasPrefix(value, "//") {
		return "/"
	}
	return value
}

func authEndpointDescriptor(rawURL string) map[string]any {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return map[string]any{"configured": true}
	}
	return map[string]any{
		"scheme": parsed.Scheme,
		"host":   parsed.Host,
		"path":   parsed.EscapedPath(),
	}
}

func localAdminEmail(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "local-dev@multi-codex.invalid"
	}
	return value
}

func readLogoutToken(r *http.Request) (string, error) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			LogoutToken string `json:"logout_token"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 2*1024*1024)).Decode(&req); err != nil {
			return "", err
		}
		token := strings.TrimSpace(req.LogoutToken)
		if token == "" {
			return "", errors.New("logout_token is required")
		}
		return token, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", err
	}
	token := strings.TrimSpace(r.Form.Get("logout_token"))
	if token == "" {
		return "", errors.New("logout_token is required")
	}
	return token, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func newOpaqueToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

func (s *Server) authSessionCookie(token string, expiresAt time.Time) *http.Cookie {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	return &http.Cookie{
		Name:     authSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.cfg.AuthCookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func (s *Server) clearAuthSessionCookie() *http.Cookie {
	return &http.Cookie{
		Name:     authSessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.AuthCookieSecure,
		SameSite: http.SameSiteLaxMode,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		fmt.Fprintf(w, `{"error":"%s"}`, err.Error())
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func notFound(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "resource not found")
		return
	}
	if errors.Is(err, store.ErrNoCapacity) {
		writeError(w, http.StatusConflict, "no executor capacity available")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "resource already exists")
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

func (s *Server) startRunError(w http.ResponseWriter, r *http.Request, err error, task domain.Task, role string) {
	if errors.Is(err, store.ErrNoCapacity) {
		priority := queuePriorityForTask(s.store, task)
		attempt := 1
		maxAttempts := retryMaxAttemptsForTask(s.store, task)
		run, queueErr := s.store.EnqueueRun(task.ID, role, task.Envelope.Executor, priority, attempt, maxAttempts, "capacity_full")
		if queueErr != nil {
			notFound(w, queueErr)
			return
		}
		s.auditHuman(r, "api.worker_enqueue", "run", run.ID, map[string]any{
			"task_id":      task.ID,
			"role":         role,
			"executor":     task.Envelope.Executor,
			"priority":     priority,
			"attempt":      attempt,
			"max_attempts": maxAttempts,
			"reason":       "capacity_full",
		})
		w.Header().Set("Retry-After", fmt.Sprint(scheduler.DefaultRetryAfterSeconds))
		writeJSON(w, http.StatusAccepted, map[string]any{
			"queued":       true,
			"run":          run,
			"backpressure": scheduler.Snapshot(s.store, task.Envelope.Executor),
		})
		return
	}
	notFound(w, err)
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

func intFromMap(values map[string]any, key string, fallback int) int {
	return intFromConfig(values, key, fallback)
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

func writeRunEventSSE(w http.ResponseWriter, event domain.RunEvent) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: message\ndata: %s\n\n", event.Seq, payload)
}

func writeNamedSSE(w http.ResponseWriter, eventName string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	if eventName == "" {
		eventName = "message"
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, payload)
}

func readArtifactContent(artifact domain.Artifact) (string, bool, error) {
	if strings.HasPrefix(artifact.Path, "memory://") {
		content, ok := artifact.Metadata["content"].(string)
		if !ok {
			return "", false, fmt.Errorf("artifact metadata does not contain textual content")
		}
		if int64(len(content)) > maxArtifactContentBytes {
			return content[:maxArtifactContentBytes], true, nil
		}
		return content, false, nil
	}

	file, err := os.Open(artifact.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, store.ErrNotFound
		}
		return "", false, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxArtifactContentBytes+1))
	if err != nil {
		return "", false, err
	}
	truncated := int64(len(data)) > maxArtifactContentBytes
	if truncated {
		data = data[:maxArtifactContentBytes]
	}
	return string(data), truncated, nil
}

func artifactContentType(artifact domain.Artifact) string {
	switch artifact.Kind {
	case "diff":
		return "text/x-diff"
	case "worker_log":
		return "text/plain"
	case "task_envelope", "result", "remote_result":
		return "application/json"
	case "prompt", "agent_override", "pr_body":
		return "text/markdown"
	default:
		return "text/plain"
	}
}

func bodyArtifactID(result map[string]any) string {
	plan, ok := result["pr_publish_plan"].(map[string]any)
	if !ok {
		return ""
	}
	value, _ := plan["body_artifact_id"].(string)
	return value
}

func (s *Server) audit(actorType string, actorID string, action string, resourceType string, resourceID string, payload map[string]any) {
	s.auditWithOrg("", actorType, actorID, action, resourceType, resourceID, payload)
}

func (s *Server) auditWithOrg(orgID string, actorType string, actorID string, action string, resourceType string, resourceID string, payload map[string]any) {
	s.store.RecordAuditLog(domain.AuditLog{
		OrgID:        orgID,
		ActorType:    actorType,
		ActorID:      actorID,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Payload:      payload,
	})
}

func (s *Server) auditHuman(r *http.Request, action string, resourceType string, resourceID string, payload map[string]any) {
	actorID := "local-dev"
	orgID := ""
	if auth, err := s.requestAuth(r); err == nil && auth.User.ID != "" {
		actorID = auth.User.ID
		orgID = auth.Membership.OrgID
	} else if strings.EqualFold(s.cfg.AuthMode, "oidc") {
		actorID = "anonymous"
	}
	s.auditWithOrg(orgID, "human", actorID, action, resourceType, resourceID, payload)
}
