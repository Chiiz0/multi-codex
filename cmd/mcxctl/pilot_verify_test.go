package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
	"github.com/Chiiz0/multi-codex/internal/workflow"
)

func TestVerifyPilotEvidenceAcceptsDryRunThenLivePR(t *testing.T) {
	st, taskID := pilotEvidenceStore(t, true)
	root := t.TempDir()
	auditReceipt := writePilotJSON(t, filepath.Join(root, "receipt.json"), `{"status":"shipped","destination":"file:///worm/pilot"}`)
	backupManifest := writePilotJSON(t, filepath.Join(root, "manifest.json"), `{"created_at":"2026-06-30T00:00:00Z","database_dump":true,"run_archive":true}`)
	restoreEvidence := writePilotJSON(t, filepath.Join(root, "restore.json"), `{"restored_at":"2026-06-30T00:10:00Z","database_restore":true,"valid":true}`)
	signoff := filepath.Join(root, "signoff.md")
	if err := os.WriteFile(signoff, []byte("service owner: approved\nsecurity: approved\noperator: approved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := verifyPilotEvidence(st, taskID, pilotVerifyOptions{
		Strict:           true,
		AuditShipReceipt: auditReceipt,
		BackupManifest:   backupManifest,
		RestoreEvidence:  restoreEvidence,
		Signoff:          signoff,
	})
	if !result.Valid {
		t.Fatalf("pilot evidence should be valid: %#v", result.Failures)
	}
	if result.PRURL != "https://github.com/example/repo/pull/77" {
		t.Fatalf("pr url = %q", result.PRURL)
	}
}

func TestVerifyPilotEvidenceRejectsMissingLivePR(t *testing.T) {
	st, taskID := pilotEvidenceStore(t, false)
	result := verifyPilotEvidence(st, taskID, pilotVerifyOptions{Strict: false})
	if result.Valid {
		t.Fatalf("pilot evidence without live PR should fail")
	}
	if !slices.Contains(result.Failures, "live_pr_published") {
		t.Fatalf("failures = %#v, want live_pr_published", result.Failures)
	}
}

func pilotEvidenceStore(t *testing.T, includeLive bool) (*store.MemoryStore, string) {
	t.Helper()
	st := store.NewMemoryStore()
	task := st.CreateTask(domain.TaskEnvelope{
		TaskID:          domain.NewID("PILOT-VERIFY"),
		ProjectID:       "proj_demo",
		RepositoryID:    "repo_demo",
		Title:           "Verify pilot evidence",
		BaseBranch:      "main",
		TargetBranch:    "codex/pilot-verify",
		Role:            "feature",
		Skill:           "company-feature-worker",
		AgentProfile:    "feature-worker-go-node",
		Executor:        "docker",
		AllowedPaths:    []string{"internal/**", "docs/**"},
		ForbiddenPaths:  []string{".env*", "secrets/**", ".github/**", "infra/**", "terraform/**", "go.sum", "pnpm-lock.yaml"},
		AllowedCommands: []string{"go test ./..."},
		RequiredOutputs: []string{"summary", "changed_files", "tests_run", "risks", "needs_human"},
		Policy: domain.TaskPolicy{
			AllowPush:             false,
			AllowDependencyChange: false,
			AllowInfraChange:      false,
			RequireAudit:          true,
			RequireTests:          true,
			RequireHumanBeforePR:  true,
		},
	})
	st.RecordAuditLog(domain.AuditLog{Action: "api.task_create", ResourceType: "task", ResourceID: task.ID, Payload: map[string]any{"task_key": task.TaskKey}})
	finishPilotRun(t, st, task.ID, "feature", map[string]any{"status": "done"})
	_, _ = st.RecordScopeCheck(task.ID, "", "main", domain.ScopeCheckResult{Status: "passed", ChangedFiles: []string{"internal/api/router.go"}})
	finishPilotRun(t, st, task.ID, "test", map[string]any{"status": "done"})
	finishPilotRun(t, st, task.ID, "audit", map[string]any{"status": "done"})
	prepareApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_prepare", Status: "pending"})
	_, _ = st.DecideApproval(prepareApproval.ID, "approved", "reviewer", "ok")
	publishApproval, _ := st.CreateApproval(domain.Approval{TaskID: task.ID, ApprovalType: "pr_publish", Status: "pending"})
	_, _ = st.DecideApproval(publishApproval.ID, "approved", "reviewer", "ok")

	prepare := finishPilotRun(t, st, task.ID, "git_sync", map[string]any{
		"status":          "prepared",
		"auto_merge":      false,
		"pr_publish_plan": workflow.RenderPRPublishPlan(task, mustRepo(t, st, task.RepositoryID), "artifact-pr-body"),
	})
	st.RecordAuditLog(domain.AuditLog{Action: "api.git_prepare_pr", ResourceType: "run", ResourceID: prepare.ID, Payload: map[string]any{"task_id": task.ID, "requires_approval": "pr_publish"}})
	dryRun := finishPilotRun(t, st, task.ID, "git_sync", map[string]any{
		"status": "publish_prepared",
		"publish_result": map[string]any{
			"dry_run":     true,
			"auto_merge":  false,
			"status":      "publish_prepared",
			"provider":    "github",
			"remote_url":  "https://github.com/example/repo.git",
			"pr_url":      "",
			"base_branch": "main",
		},
		"auto_merge": false,
	})
	st.RecordAuditLog(domain.AuditLog{Action: "api.git_publish_pr", ResourceType: "run", ResourceID: dryRun.ID, Payload: map[string]any{"task_id": task.ID, "dry_run": true, "auto_merge": false}})
	if includeLive {
		live := finishPilotRun(t, st, task.ID, "git_sync", map[string]any{
			"status": "published",
			"publish_result": map[string]any{
				"dry_run":             false,
				"auto_merge":          false,
				"credential_resolved": true,
				"status":              "published",
				"provider":            "github",
				"pr_url":              "https://github.com/example/repo/pull/77",
			},
			"auto_merge": false,
		})
		st.RecordAuditLog(domain.AuditLog{Action: "api.git_publish_pr", ResourceType: "run", ResourceID: live.ID, Payload: map[string]any{"task_id": task.ID, "dry_run": false, "credential_resolved": true, "auto_merge": false}})
	}
	return st, task.ID
}

func finishPilotRun(t *testing.T, st *store.MemoryStore, taskID string, role string, result map[string]any) domain.Run {
	t.Helper()
	run, err := st.StartRun(taskID, role, "docker")
	if err != nil {
		t.Fatal(err)
	}
	finished, err := st.FinishRun(run.ID, "succeeded", result)
	if err != nil {
		t.Fatal(err)
	}
	return finished
}

func mustRepo(t *testing.T, st *store.MemoryStore, repoID string) domain.Repository {
	t.Helper()
	repo, err := st.GetRepository(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func writePilotJSON(t *testing.T, path string, value string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(value+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
