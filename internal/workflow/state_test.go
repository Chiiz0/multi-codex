package workflow

import (
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func TestBuildStateFullGatePath(t *testing.T) {
	st := store.NewMemoryStore()
	task := st.CreateTask(testEnvelope())

	assertNextAction(t, BuildState(st, task), "worker_spawn")

	feature, _ := st.StartRun(task.ID, "feature", "docker")
	_, _ = st.FinishRun(feature.ID, "succeeded", map[string]any{"status": "done"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "repo_scope_check")

	_, _ = st.RecordScopeCheck(task.ID, feature.ID, "origin/main", domain.ScopeCheckResult{Status: "passed", ChangedFiles: []string{"internal/api/router.go"}, Violations: []string{}})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "test_run_required")

	testRun, _ := st.StartRun(task.ID, "test", "docker")
	_, _ = st.FinishRun(testRun.ID, "succeeded", map[string]any{"status": "done"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "audit_run")

	auditRun, _ := st.StartRun(task.ID, "audit", "docker")
	_, _ = st.FinishRun(auditRun.ID, "succeeded", map[string]any{"status": "done"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "approval_request")

	approval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_prepare", Status: "pending"})
	_, _ = st.DecideApproval(approval.ID, "approved", "", "ok")

	state := BuildState(st, mustTask(t, st, task.ID))
	if !state.ReadyForPR {
		t.Fatalf("expected task to be ready for PR")
	}
	assertNextAction(t, state, "git_prepare_pr")

	gitPrepareRun, _ := st.StartRun(task.ID, "git_sync", "docker")
	_, _ = st.FinishRun(gitPrepareRun.ID, "succeeded", map[string]any{"status": "prepared"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "approval_request_pr_publish")

	publishApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_publish", Status: "pending"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "approval_status")
	_, _ = st.DecideApproval(publishApproval.ID, "approved", "", "ok")
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "git_publish_pr")

	gitPublishRun, _ := st.StartRun(task.ID, "git_sync", "docker")
	_, _ = st.FinishRun(gitPublishRun.ID, "succeeded", map[string]any{"status": "publish_prepared"})
	assertNextAction(t, BuildState(st, mustTask(t, st, task.ID)), "completed")
}

func TestBuildStateBlockers(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*store.MemoryStore, domain.Task)
		wantBlock string
	}{
		{
			name: "scope violation",
			mutate: func(st *store.MemoryStore, task domain.Task) {
				_, _ = st.RecordScopeCheck(task.ID, "", "origin/main", domain.ScopeCheckResult{Status: "blocked", ChangedFiles: []string{".env"}, Violations: []string{".env matches forbidden_paths"}})
			},
			wantBlock: "scope violation blocks test, audit, and git sync",
		},
		{
			name: "test failed",
			mutate: func(st *store.MemoryStore, task domain.Task) {
				run, _ := st.StartRun(task.ID, "test", "docker")
				_, _ = st.FinishRun(run.ID, "failed", map[string]any{"status": "failed"})
			},
			wantBlock: "test failed or was blocked",
		},
		{
			name: "audit blocker",
			mutate: func(st *store.MemoryStore, task domain.Task) {
				run, _ := st.StartRun(task.ID, "audit", "docker")
				_, _ = st.FinishRun(run.ID, "blocked", map[string]any{"status": "blocked"})
			},
			wantBlock: "audit blocker present",
		},
		{
			name: "approval rejected",
			mutate: func(st *store.MemoryStore, task domain.Task) {
				approval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_prepare", Status: "pending"})
				_, _ = st.DecideApproval(approval.ID, "rejected", "", "no")
			},
			wantBlock: "approval rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.NewMemoryStore()
			task := st.CreateTask(testEnvelope())
			tt.mutate(st, task)
			state := BuildState(st, mustTask(t, st, task.ID))
			if len(state.BlockedReasons) == 0 || state.BlockedReasons[0] != tt.wantBlock {
				t.Fatalf("blocked reasons = %#v, want first %q", state.BlockedReasons, tt.wantBlock)
			}
			if ok, _ := GateAllows(state, "git_prepare_pr"); ok {
				t.Fatalf("git_prepare_pr should be blocked")
			}
		})
	}
}

func testEnvelope() domain.TaskEnvelope {
	return domain.TaskEnvelope{
		TaskID:          domain.NewID("TEST"),
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Workflow test",
		BaseBranch:      "origin/main",
		TargetBranch:    "codex/workflow-test",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**", "docs/**"},
		ForbiddenPaths:  []string{".env*", "secrets/**"},
		AllowedCommands: []string{"go test ./..."},
		Policy: domain.TaskPolicy{
			RequireAudit:         true,
			RequireTests:         true,
			RequireHumanBeforePR: true,
		},
	}
}

func mustTask(t *testing.T, st store.Store, taskID string) domain.Task {
	t.Helper()
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	return task
}

func assertNextAction(t *testing.T, state domain.WorkflowState, action string) {
	t.Helper()
	for _, next := range state.NextActions {
		if next == action {
			return
		}
	}
	t.Fatalf("next actions = %#v, want %q", state.NextActions, action)
}
