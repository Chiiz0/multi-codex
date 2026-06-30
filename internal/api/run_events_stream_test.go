package api

import (
	"bufio"
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

func TestRunEventStreamSendsNewEventsAndAudits(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          "EVENT-STREAM-1",
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Run event stream",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/event-stream-1",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	})
	run, err := st.StartRun(task.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	if _, err := st.AddEvent(run.ID, "info", "initial", "initial event", nil); err != nil {
		t.Fatalf("add initial event: %v", err)
	}

	server := NewServer(config.Config{QueueEnabled: false}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()
	cookie := localSessionCookie(t, server.Handler())

	req, err := http.NewRequest(http.MethodGet, testServer.URL+"/api/v1/runs/"+run.ID+"/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := testServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	if body := readAPIStreamUntil(t, reader, `"event_type":"initial"`, time.Second); !strings.Contains(body, "id: ") {
		t.Fatalf("initial stream body = %s", body)
	}

	if _, err := st.AddEvent(run.ID, "info", "streamed", "streamed event", nil); err != nil {
		t.Fatalf("add streamed event: %v", err)
	}
	if body := readAPIStreamUntil(t, reader, `"event_type":"streamed"`, 3*time.Second); !strings.Contains(body, "id: ") {
		t.Fatalf("streamed body = %s", body)
	}
	_ = resp.Body.Close()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hasAuditAction(st, "api.run_event_stream_open", run.ID) && hasAuditAction(st, "api.run_event_stream_close", run.ID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected run event stream open and close audit rows")
}

func readAPIStreamUntil(t *testing.T, reader *bufio.Reader, needle string, timeout time.Duration) string {
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

func hasAuditAction(st *store.MemoryStore, action string, resourceID string) bool {
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == action && entry.ResourceID == resourceID {
			return true
		}
	}
	return false
}
