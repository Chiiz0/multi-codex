package scheduler

import (
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestSnapshotShowsFullAndReleasedCapacity(t *testing.T) {
	st := store.NewMemoryStore()
	first := st.CreateTask(envelope("BACKPRESSURE-1"))
	second := st.CreateTask(envelope("BACKPRESSURE-2"))

	run, err := st.StartRun(first.ID, "feature", "docker")
	if err != nil {
		t.Fatalf("start first run: %v", err)
	}
	if _, err := st.StartRun(second.ID, "feature", "docker"); err == nil {
		t.Fatalf("expected second run to hit capacity")
	}

	full := Snapshot(st, "docker")
	if full.RetryAfterSeconds != DefaultRetryAfterSeconds {
		t.Fatalf("retry after = %d", full.RetryAfterSeconds)
	}
	if full.AvailableSlots != 0 {
		t.Fatalf("available slots while full = %d", full.AvailableSlots)
	}
	if len(full.Nodes) == 0 || full.Nodes[0].IneligibleReason != "capacity_full" {
		t.Fatalf("unexpected node state while full: %#v", full.Nodes)
	}

	if _, err := st.FinishRun(run.ID, "succeeded", map[string]any{"status": "done"}); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	released := Snapshot(st, "docker")
	if released.AvailableSlots != 1 {
		t.Fatalf("available slots after release = %d", released.AvailableSlots)
	}
	if len(released.Nodes) == 0 || !released.Nodes[0].Eligible {
		t.Fatalf("unexpected node state after release: %#v", released.Nodes)
	}
}

func TestSnapshotRanksNodesByUtilizationAndAvailableSlots(t *testing.T) {
	st := store.NewMemoryStore()
	if _, err := st.RegisterExecutorNode(domain.ExecutorNode{
		Kind:     "docker",
		Name:     "large-docker",
		Status:   "active",
		Capacity: map[string]any{"concurrency": 4},
		Labels:   map[string]any{},
	}); err != nil {
		t.Fatalf("register large node: %v", err)
	}

	snapshot := Snapshot(st, "docker")
	if snapshot.AvailableSlots != 5 {
		t.Fatalf("available slots = %d, want 5", snapshot.AvailableSlots)
	}
	if len(snapshot.Nodes) < 2 {
		t.Fatalf("expected at least two docker nodes, got %#v", snapshot.Nodes)
	}
	first := snapshot.Nodes[0]
	if first.Name != "large-docker" || first.SelectionRank != 1 || first.AvailableSlots != 4 || first.Utilization != 0 {
		t.Fatalf("unexpected first node = %#v", first)
	}

	task := st.CreateTask(envelope("BACKPRESSURE-BALANCE"))
	if _, err := st.StartRun(task.ID, "feature", "docker"); err != nil {
		t.Fatalf("start run: %v", err)
	}
	snapshot = Snapshot(st, "docker")
	if snapshot.Nodes[0].Name == "large-docker" {
		t.Fatalf("expected idle small node to rank before partially used large node: %#v", snapshot.Nodes)
	}
	if snapshot.Nodes[1].Name != "large-docker" || snapshot.Nodes[1].Utilization != 0.25 {
		t.Fatalf("unexpected large-node utilization after run: %#v", snapshot.Nodes)
	}
}

func envelope(taskKey string) domain.TaskEnvelope {
	return domain.TaskEnvelope{
		TaskID:          taskKey,
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Backpressure test",
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
