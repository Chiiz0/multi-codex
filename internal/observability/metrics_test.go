package observability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestPrometheusTextNormalizesDynamicPaths(t *testing.T) {
	metrics := NewMetrics("multi-codex-test")
	metrics.Record("GET", normalizeMetricPath("/api/v1/runs/019f0207-36e1-7791-a8bd-3cecd85e73e5/artifacts"), 200, 25*time.Millisecond, "trace-1")

	text := metrics.PrometheusText()
	if !strings.Contains(text, `path="/api/v1/runs/{id}/artifacts"`) {
		t.Fatalf("expected normalized path in metrics text, got:\n%s", text)
	}
	if strings.Contains(text, "019f0207-36e1-7791-a8bd-3cecd85e73e5") {
		t.Fatalf("metrics text leaked raw dynamic id:\n%s", text)
	}
	if !strings.Contains(text, `multi_codex_http_request_duration_seconds_bucket{service="multi-codex-test",method="GET",path="/api/v1/runs/{id}/artifacts",le="0.025"} 1`) {
		t.Fatalf("expected HTTP duration histogram bucket, got:\n%s", text)
	}
}

func TestMiddlewareUsesTraceparent(t *testing.T) {
	metrics := NewMetrics("multi-codex-test")
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := TraceID(r.Context()); got != traceID {
			t.Fatalf("trace id = %q, want %q", got, traceID)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	request.Header.Set("Traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Header().Get("X-Multi-Codex-Trace-Id") != traceID {
		t.Fatalf("response trace id = %q", recorder.Header().Get("X-Multi-Codex-Trace-Id"))
	}
	if !strings.Contains(recorder.Header().Get("Traceparent"), traceID) {
		t.Fatalf("response traceparent = %q", recorder.Header().Get("Traceparent"))
	}
}

func TestOTLPExportContainsMetricsAndNormalizesPaths(t *testing.T) {
	metrics := NewMetrics("multi-codex-test")
	metrics.Record("GET", normalizeMetricPath("/api/v1/runs/019f0207-36e1-7791-a8bd-3cecd85e73e5/artifacts"), 200, 25*time.Millisecond, "4bf92f3577b34da6a3ce929d0e0e4736")
	export := metrics.OTLPExport([]domain.Run{{Role: "feature", Executor: "docker", Status: "running"}})
	data, err := json.Marshal(export)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, needle := range []string{"resourceMetrics", "multi_codex_http_requests_total", "multi_codex_http_request_duration_seconds", "multi_codex_active_runs", "multi_codex_run_duration_seconds", "/api/v1/runs/{id}/artifacts"} {
		if !strings.Contains(text, needle) {
			t.Fatalf("OTLP export missing %q:\n%s", needle, text)
		}
	}
	if strings.Contains(text, "019f0207-36e1-7791-a8bd-3cecd85e73e5") {
		t.Fatalf("OTLP export leaked raw dynamic id:\n%s", text)
	}
}

func TestRunMetricsSnapshotGroupsRuns(t *testing.T) {
	start := time.Now().Add(-2 * time.Second)
	finish := time.Now()
	metrics := RunMetricsSnapshot([]domain.Run{
		{Role: "feature", Executor: "docker", Status: "running", StartedAt: &start},
		{Role: "feature", Executor: "docker", Status: "succeeded", StartedAt: &start, FinishedAt: &finish},
	})

	if len(metrics) != 2 {
		t.Fatalf("metrics length = %d", len(metrics))
	}
	if metrics[0].Role != "feature" || metrics[0].Executor != "docker" {
		t.Fatalf("unexpected first metric = %#v", metrics[0])
	}
	text := RunsPrometheusText("multi-codex-test", []domain.Run{
		{Role: "feature", Executor: "docker", Status: "running", StartedAt: &start},
		{Role: "feature", Executor: "docker", Status: "succeeded", StartedAt: &start, FinishedAt: &finish},
	})
	if !strings.Contains(text, `multi_codex_active_runs{service="multi-codex-test",role="feature",executor="docker",status="running"} 1`) {
		t.Fatalf("expected active run metric, got:\n%s", text)
	}
	if !strings.Contains(text, `multi_codex_run_duration_seconds_bucket{service="multi-codex-test",role="feature",executor="docker",status="succeeded",le="5"} 1`) {
		t.Fatalf("expected run duration histogram bucket, got:\n%s", text)
	}
}

func TestOperationsPrometheusTextIncludesQueueAndAuditSignals(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	text := OperationsPrometheusText("multi-codex-test", []domain.Run{
		{Executor: "docker", Status: "queued"},
		{Executor: "docker", Status: "queued"},
		{Executor: "ssh", Status: "failed"},
		{Executor: "docker", Status: "timed_out"},
	}, []domain.AuditLog{
		{Action: "api.audit_ship_failed", CreatedAt: now},
		{Action: "api.retention_cleanup_failed", CreatedAt: now.Add(time.Minute)},
	})

	for _, needle := range []string{
		`multi_codex_queue_depth{service="multi-codex-test",executor="docker"} 2`,
		`multi_codex_worker_terminal_failures{service="multi-codex-test",status="failed"} 1`,
		`multi_codex_worker_terminal_failures{service="multi-codex-test",status="timed_out"} 1`,
		`multi_codex_audit_action_total{service="multi-codex-test",action="api.audit_ship_failed"} 1`,
		`multi_codex_audit_action_last_seen_timestamp_seconds{service="multi-codex-test",action="api.retention_cleanup_failed"} 1060`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("operations metrics missing %q:\n%s", needle, text)
		}
	}
}
