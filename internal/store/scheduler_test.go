package store

import (
	"errors"
	"testing"
	"time"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestMemoryStartRunRespectsExecutorNodeConcurrency(t *testing.T) {
	st := NewMemoryStore()
	first := st.CreateTask(schedulerEnvelope("SCHED-1"))
	second := st.CreateTask(schedulerEnvelope("SCHED-2"))

	run, err := st.StartRun(first.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if run.ExecutorNodeID == "" {
		t.Fatalf("expected first run to have an executor node")
	}
	if _, err := st.StartRun(second.ID, "feature", "docker"); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("start second run error = %v, want ErrNoCapacity", err)
	}
	if _, err := st.FinishRun(run.ID, "succeeded", map[string]any{"status": "done"}); err != nil {
		t.Fatalf("finish first run: %v", err)
	}
	if _, err := st.StartRun(second.ID, "feature", "docker"); err != nil {
		t.Fatalf("start second run after release: %v", err)
	}
}

func TestMemoryStartRunBalancesByUtilizationThenAvailableSlots(t *testing.T) {
	st := NewMemoryStore()
	seen := time.Now().UTC()
	large, err := st.RegisterExecutorNode(domain.ExecutorNode{
		Kind:       "docker",
		Name:       "large-docker",
		Status:     "active",
		Capacity:   map[string]any{"concurrency": 4},
		LastSeenAt: &seen,
		Labels:     map[string]any{},
	})
	if err != nil {
		t.Fatalf("register large node: %v", err)
	}
	defaultDockerID := ""
	for _, node := range st.ListExecutorNodes() {
		if node.Kind == "docker" && node.ID != large.ID {
			defaultDockerID = node.ID
			break
		}
	}
	if defaultDockerID == "" {
		t.Fatalf("default docker node not found")
	}

	first := st.CreateTask(schedulerEnvelope("BALANCE-1"))
	firstRun, err := st.StartRun(first.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if firstRun.ExecutorNodeID != large.ID {
		t.Fatalf("first run node = %q, want high-capacity node %q", firstRun.ExecutorNodeID, large.ID)
	}

	second := st.CreateTask(schedulerEnvelope("BALANCE-2"))
	secondRun, err := st.StartRun(second.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start second run: %v", err)
	}
	if secondRun.ExecutorNodeID != defaultDockerID {
		t.Fatalf("second run node = %q, want idle default node %q", secondRun.ExecutorNodeID, defaultDockerID)
	}
}

func TestMemoryQueueDispatchOrdersByPriority(t *testing.T) {
	st := NewMemoryStore()
	low := st.CreateTask(schedulerEnvelope("QUEUE-LOW"))
	high := st.CreateTask(schedulerEnvelope("QUEUE-HIGH"))
	if _, err := st.EnqueueRun(low.ID, "feature", "docker", 1, 1, 1, "test_low"); err != nil {
		t.Fatalf("enqueue low: %v", err)
	}
	if _, err := st.EnqueueRun(high.ID, "feature", "docker", 10, 1, 1, "test_high"); err != nil {
		t.Fatalf("enqueue high: %v", err)
	}
	dispatched, err := st.DispatchQueuedRun()
	if err != nil {
		t.Fatalf("dispatch queued run: %v", err)
	}
	if dispatched.TaskID != high.ID {
		t.Fatalf("dispatched task = %q, want high priority task %q", dispatched.TaskID, high.ID)
	}
	if dispatched.Status != "running" || dispatched.ExecutorNodeID == "" {
		t.Fatalf("unexpected dispatched run = %#v", dispatched)
	}
	events := st.ListEvents(dispatched.ID)
	if len(events) < 2 || events[0].EventType != "worker_queued" || events[1].EventType != "worker_spawn" {
		t.Fatalf("unexpected queue events = %#v", events)
	}
}

func schedulerEnvelope(taskKey string) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		TaskID:          taskKey,
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Scheduler test",
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
