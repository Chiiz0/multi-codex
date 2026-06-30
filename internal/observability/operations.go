package observability

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

var operationalAuditActions = []string{
	"api.audit_ship",
	"api.audit_ship_failed",
	"api.retention_cleanup",
	"api.retention_cleanup_failed",
	"api.telemetry_push_failed",
	"mcp.telemetry_push_failed",
}

type AuditActionMetric struct {
	Action     string     `json:"action"`
	Count      int64      `json:"count"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

func OperationsSnapshot(runs []domain.Run, auditLogs []domain.AuditLog) map[string]any {
	queueDepth := map[string]int64{}
	workerFailures := map[string]int64{}
	for _, run := range runs {
		executor := run.Executor
		if executor == "" {
			executor = "unknown"
		}
		switch run.Status {
		case "queued":
			queueDepth[executor]++
		case "failed", "timed_out", "blocked":
			workerFailures[run.Status]++
		}
	}

	actions := auditActionMetrics(auditLogs)
	return map[string]any{
		"queue_depth":     queueDepth,
		"worker_failures": workerFailures,
		"audit_actions":   actions,
	}
}

func OperationsPrometheusText(service string, runs []domain.Run, auditLogs []domain.AuditLog) string {
	snapshot := OperationsSnapshot(runs, auditLogs)
	queueDepth, _ := snapshot["queue_depth"].(map[string]int64)
	workerFailures, _ := snapshot["worker_failures"].(map[string]int64)
	auditActions, _ := snapshot["audit_actions"].([]AuditActionMetric)

	var b strings.Builder
	fmt.Fprintf(&b, "# HELP multi_codex_queue_depth Current queued worker runs by executor.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_queue_depth gauge\n")
	writeLabeledGauge(&b, service, "multi_codex_queue_depth", "executor", queueDepth)

	fmt.Fprintf(&b, "# HELP multi_codex_worker_terminal_failures Current terminal worker failures by status.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_worker_terminal_failures gauge\n")
	writeLabeledGauge(&b, service, "multi_codex_worker_terminal_failures", "status", workerFailures)

	fmt.Fprintf(&b, "# HELP multi_codex_audit_action_total Audit action count for operational health actions.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_audit_action_total counter\n")
	for _, action := range auditActions {
		fmt.Fprintf(&b, "multi_codex_audit_action_total{service=%q,action=%q} %d\n", service, action.Action, action.Count)
	}
	fmt.Fprintf(&b, "# HELP multi_codex_audit_action_last_seen_timestamp_seconds Last observed audit action timestamp.\n")
	fmt.Fprintf(&b, "# TYPE multi_codex_audit_action_last_seen_timestamp_seconds gauge\n")
	for _, action := range auditActions {
		var timestamp int64
		if action.LastSeenAt != nil {
			timestamp = action.LastSeenAt.Unix()
		}
		fmt.Fprintf(&b, "multi_codex_audit_action_last_seen_timestamp_seconds{service=%q,action=%q} %d\n", service, action.Action, timestamp)
	}
	return b.String()
}

func auditActionMetrics(auditLogs []domain.AuditLog) []AuditActionMetric {
	interesting := map[string]bool{}
	for _, action := range operationalAuditActions {
		interesting[action] = true
	}
	counts := map[string]int64{}
	lastSeen := map[string]time.Time{}
	for _, entry := range auditLogs {
		if !interesting[entry.Action] {
			continue
		}
		counts[entry.Action]++
		if entry.CreatedAt.After(lastSeen[entry.Action]) {
			lastSeen[entry.Action] = entry.CreatedAt
		}
	}
	metrics := make([]AuditActionMetric, 0, len(operationalAuditActions))
	for _, action := range operationalAuditActions {
		metric := AuditActionMetric{Action: action, Count: counts[action]}
		if at, ok := lastSeen[action]; ok {
			metric.LastSeenAt = &at
		}
		metrics = append(metrics, metric)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].Action < metrics[j].Action })
	return metrics
}

func writeLabeledGauge(b *strings.Builder, service string, metric string, label string, values map[string]int64) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "%s{service=%q,%s=%q} %d\n", metric, service, label, key, values[key])
	}
}
