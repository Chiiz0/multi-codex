package mcp

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
	"github.com/Chiiz0/multi-codex/internal/workflow"
)

func TestGitPublishPRToolAuditsCredentialMetadata(t *testing.T) {
	st := store.NewMemoryStore()
	task := mcpGitPublishReadyTask(t, st)
	server := NewServer(config.Config{GitSyncMode: "dry-run"}, st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	resp := callMCPToolForTest(t, server, "git_publish_pr", map[string]any{"task_id": task.ID})
	result := resp["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	run := structured["run"].(map[string]any)
	runID, _ := run["id"].(string)
	if runID == "" || run["status"] != "succeeded" {
		t.Fatalf("run = %#v", run)
	}

	assertMCPGitPublishEvent(t, st, runID)
	assertMCPGitPublishAudit(t, st, runID)
}

func assertMCPGitPublishEvent(t *testing.T, st store.Store, runID string) {
	t.Helper()
	for _, event := range st.ListEvents(runID) {
		if event.EventType != "git_publish_pr" {
			continue
		}
		if event.Payload["credential_provider"] != "env" {
			t.Fatalf("credential provider event payload = %#v", event.Payload)
		}
		if resolved, ok := event.Payload["credential_resolved"].(bool); !ok || resolved {
			t.Fatalf("credential resolved event payload = %#v", event.Payload)
		}
		if event.Payload["auto_merge"] != false {
			t.Fatalf("auto_merge event payload = %#v", event.Payload)
		}
		return
	}
	t.Fatalf("expected git_publish_pr run event")
}

func assertMCPGitPublishAudit(t *testing.T, st store.Store, runID string) {
	t.Helper()
	for _, entry := range st.ListAuditLogs() {
		if entry.Action != "mcp.git_publish_pr" || entry.ResourceID != runID {
			continue
		}
		if entry.Payload["credential_provider"] != "env" {
			t.Fatalf("credential provider audit payload = %#v", entry.Payload)
		}
		if resolved, ok := entry.Payload["credential_resolved"].(bool); !ok || resolved {
			t.Fatalf("credential resolved audit payload = %#v", entry.Payload)
		}
		if entry.Payload["auto_merge"] != false {
			t.Fatalf("auto_merge audit payload = %#v", entry.Payload)
		}
		return
	}
	t.Fatalf("expected mcp.git_publish_pr audit row")
}

func mcpGitPublishReadyTask(t *testing.T, st *store.MemoryStore) domain.Task {
	t.Helper()
	repo := st.CreateRepository(domain.Repository{
		ProjectID:     "proj_demo",
		Name:          "github-repo",
		Provider:      "github",
		RemoteURL:     "https://github.com/example/repo.git",
		DefaultBranch: "main",
	})
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          domain.NewID("MCP-GIT-PUBLISH"),
		ProjectID:       "proj_demo",
		RepositoryID:    repo.ID,
		Title:           "Publish PR metadata",
		BaseBranch:      "main",
		TargetBranch:    "codex/publish-pr-metadata",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**"},
		ForbiddenPaths:  []string{".env*"},
		AllowedCommands: []string{"go test ./..."},
		Policy: domain.TaskPolicy{
			RequireAudit:         true,
			RequireTests:         true,
			RequireHumanBeforePR: true,
		},
	})
	feature, _ := st.StartRun(task.ID, "feature", "docker")
	_, _ = st.FinishRun(feature.ID, "succeeded", map[string]any{"status": "done"})
	_, _ = st.RecordScopeCheck(task.ID, feature.ID, "main", domain.ScopeCheckResult{Status: "passed", ChangedFiles: []string{"internal/mcp/server.go"}})
	testRun, _ := st.StartRun(task.ID, "test", "docker")
	_, _ = st.FinishRun(testRun.ID, "succeeded", map[string]any{"status": "done"})
	auditRun, _ := st.StartRun(task.ID, "audit", "docker")
	_, _ = st.FinishRun(auditRun.ID, "succeeded", map[string]any{"status": "done"})
	prepareApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_prepare", Status: "pending"})
	_, _ = st.DecideApproval(prepareApproval.ID, "approved", "reviewer", "ok")
	bodyArtifactID := domain.NewID("artifact")
	prepareRun, _ := st.StartRun(task.ID, "git_sync", "docker")
	_, _ = st.FinishRun(prepareRun.ID, "succeeded", map[string]any{
		"status":          "prepared",
		"pr_body":         "PR body",
		"pr_publish_plan": workflow.RenderPRPublishPlan(task, repo, bodyArtifactID),
	})
	publishApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_publish", Status: "pending"})
	_, _ = st.DecideApproval(publishApproval.ID, "approved", "reviewer", "ok")
	return task
}
