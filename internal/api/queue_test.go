package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestQueueAPIDispatchStartsQueuedRunAndAudits(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(apiQueueEnvelope("API-QUEUE-1"))
	queued, err := st.EnqueueRun(task.ID, "feature", "docker", 5, 1, 2, "test_capacity")
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	server := NewServer(config.Config{
		RunRoot:       t.TempDir(),
		WorktreeRoot:  t.TempDir(),
		RepoCacheRoot: t.TempDir(),
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/queue", nil)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("queue status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var snapshot struct {
		QueuedRuns []domain.Run `json:"queued_runs"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode queue status: %v", err)
	}
	if len(snapshot.QueuedRuns) != 1 || snapshot.QueuedRuns[0].ID != queued.ID {
		t.Fatalf("queued runs = %#v", snapshot.QueuedRuns)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/queue/dispatch", nil)
	resp = httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("dispatch status = %d, body = %s", resp.Code, resp.Body.String())
	}
	var payload struct {
		Run domain.Run `json:"run"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode dispatch: %v", err)
	}
	if payload.Run.ID != queued.ID || payload.Run.Status != "running" || payload.Run.ExecutorNodeID == "" {
		t.Fatalf("dispatched run = %#v", payload.Run)
	}

	actions := map[string]bool{}
	for _, entry := range st.ListAuditLogs() {
		actions[entry.Action] = true
	}
	for _, action := range []string{"api.queue_dispatch", "api.queue_dispatch_manual"} {
		if !actions[action] {
			t.Fatalf("expected %s audit row, actions = %#v", action, actions)
		}
	}
	waitForAPIQueueRunTerminal(t, st, queued.ID)
}

func apiQueueEnvelope(taskKey string) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		TaskID:          taskKey,
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Queue API test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/" + taskKey,
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
	}
}

func waitForAPIQueueRunTerminal(t *testing.T, st store.Store, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status != "queued" && run.Status != "preparing" && run.Status != "running" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := st.GetRun(runID)
	t.Fatalf("run did not reach terminal status: %#v", run)
}
