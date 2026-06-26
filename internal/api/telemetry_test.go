package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestAPITelemetryPushPostsOTLP(t *testing.T) {
	var received string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		received = string(data)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer collector.Close()

	st := store.NewMemoryStore()
	server := NewServer(config.Config{
		TelemetryPushURL: collector.URL,
		RunRoot:          t.TempDir(),
		WorktreeRoot:     t.TempDir(),
		RepoCacheRoot:    t.TempDir(),
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.metrics.Record(http.MethodGet, "/healthz", http.StatusOK, 0, "trace-1")

	server.pushTelemetry("test")

	if !strings.Contains(received, "resourceMetrics") || !strings.Contains(received, "multi_codex_http_request_duration_seconds") {
		t.Fatalf("unexpected telemetry payload: %s", received)
	}
}
