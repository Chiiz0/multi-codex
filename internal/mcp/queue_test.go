package mcp

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestQueueToolsStatusAndDispatch(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(mcpQueueEnvelope("MCP-QUEUE-1"))
	queued, err := st.EnqueueRun(task.ID, "feature", "docker", 9, 1, 2, "test_capacity")
	if err != nil {
		t.Fatalf("enqueue run: %v", err)
	}
	server := NewServer(config.Config{
		RunRoot:       t.TempDir(),
		WorktreeRoot:  t.TempDir(),
		RepoCacheRoot: t.TempDir(),
	}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	statusResp := callMCPToolForTest(t, server, "queue_status", map[string]any{})
	statusResult := statusResp["result"].(map[string]any)
	statusStructured := statusResult["structuredContent"].(map[string]any)
	queuedRuns := statusStructured["queued_runs"].([]any)
	if len(queuedRuns) != 1 {
		t.Fatalf("queued runs = %#v", queuedRuns)
	}

	dispatchResp := callMCPToolForTest(t, server, "queue_dispatch", map[string]any{})
	dispatchResult := dispatchResp["result"].(map[string]any)
	dispatchStructured := dispatchResult["structuredContent"].(map[string]any)
	run := dispatchStructured["run"].(map[string]any)
	if run["id"] != queued.ID || run["status"] != "running" || run["executor_node_id"] == "" {
		t.Fatalf("dispatched run = %#v", run)
	}

	var foundToolCall bool
	for _, call := range st.ListToolCalls() {
		if call.ToolName == "queue_dispatch" && call.Status == "succeeded" {
			foundToolCall = true
			break
		}
	}
	if !foundToolCall {
		t.Fatalf("expected queue_dispatch tool call record")
	}
	var foundAudit bool
	for _, entry := range st.ListAuditLogs() {
		if entry.Action == "mcp.queue_dispatch" && entry.ResourceID == queued.ID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected mcp.queue_dispatch audit row")
	}
	waitForMCPQueueRunTerminal(t, st, queued.ID)
}

func mcpQueueEnvelope(taskKey string) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		TaskID:          taskKey,
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Queue MCP test",
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

func waitForMCPQueueRunTerminal(t *testing.T, st store.Store, runID string) {
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
