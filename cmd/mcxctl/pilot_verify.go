package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Chiiz0/multi-codex/internal/config"
	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/policy"
	"github.com/Chiiz0/multi-codex/internal/store"
)

type pilotVerifyOptions struct {
	Strict           bool   `json:"strict"`
	AuditShipReceipt string `json:"audit_ship_receipt,omitempty"`
	BackupManifest   string `json:"backup_manifest,omitempty"`
	RestoreEvidence  string `json:"restore_evidence,omitempty"`
	Signoff          string `json:"signoff,omitempty"`
}

type pilotVerification struct {
	Valid      bool               `json:"valid"`
	TaskID     string             `json:"task_id"`
	TaskKey    string             `json:"task_key,omitempty"`
	ProjectID  string             `json:"project_id,omitempty"`
	Repository string             `json:"repository,omitempty"`
	PRURL      string             `json:"pr_url,omitempty"`
	DryRunID   string             `json:"dry_run_id,omitempty"`
	LiveRunID  string             `json:"live_run_id,omitempty"`
	Options    pilotVerifyOptions `json:"options"`
	Checks     []pilotVerifyCheck `json:"checks"`
	Failures   []string           `json:"failures,omitempty"`
	Generated  time.Time          `json:"generated_at"`
}

type pilotVerifyCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail any    `json:"detail,omitempty"`
}

func pilotVerify(log *slog.Logger, args []string) {
	cfg := config.FromEnv()
	fs := flag.NewFlagSet("pilot-verify", flag.ExitOnError)
	databaseURL := fs.String("database-url", cfg.DatabaseURL, "PostgreSQL connection URL")
	taskID := fs.String("task-id", "", "pilot task id to verify")
	strict := fs.Bool("strict", true, "require audit ship, backup, restore, and sign-off evidence files")
	auditShipReceipt := fs.String("audit-ship-receipt", "", "audit-ship receipt JSON path")
	backupManifest := fs.String("backup-manifest", "", "backup manifest JSON path")
	restoreEvidence := fs.String("restore-evidence", "", "restore drill evidence JSON path")
	signoff := fs.String("signoff", "", "pilot sign-off file path")
	_ = fs.Parse(args)
	if strings.TrimSpace(*taskID) == "" {
		log.Error("pilot task id is required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtimeStore, err := store.Open(ctx, *databaseURL, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		log.Error("open store failed", "error", err)
		os.Exit(1)
	}
	defer runtimeStore.Close()

	result := verifyPilotEvidence(runtimeStore.Store, *taskID, pilotVerifyOptions{
		Strict:           *strict,
		AuditShipReceipt: *auditShipReceipt,
		BackupManifest:   *backupManifest,
		RestoreEvidence:  *restoreEvidence,
		Signoff:          *signoff,
	})
	writeJSONStdout(result)
	if !result.Valid {
		os.Exit(1)
	}
}

func verifyPilotEvidence(st store.Store, taskID string, opts pilotVerifyOptions) pilotVerification {
	result := pilotVerification{Valid: true, TaskID: taskID, Options: opts, Generated: time.Now().UTC()}
	task, err := st.GetTask(taskID)
	if err != nil {
		result.add("task_exists", false, err.Error())
		return result
	}
	result.TaskKey = task.TaskKey
	result.ProjectID = task.ProjectID
	result.add("task_exists", true, task.TaskKey)

	project, err := st.GetProject(task.ProjectID)
	result.add("project_exists", err == nil && project.OrgID != "", map[string]any{"project_id": task.ProjectID, "org_id": project.OrgID})
	repo, err := st.GetRepository(task.RepositoryID)
	if err == nil {
		result.Repository = repo.RemoteURL
	}
	result.add("repository_exists", err == nil && repo.ProjectID == task.ProjectID, map[string]any{"repository_id": task.RepositoryID, "project_id": repo.ProjectID})

	validation := policy.ValidateTaskWithResources(st, task.Envelope)
	result.add("policy_validation", validation.Valid, validation)
	result.add("task_policy_requires_tests_audit_approval", task.Envelope.Policy.RequireTests && task.Envelope.Policy.RequireAudit && task.Envelope.Policy.RequireHumanBeforePR && !task.Envelope.Policy.AllowPush, task.Envelope.Policy)

	runs := st.ListRuns(task.ID)
	result.add("feature_run_succeeded", latestSucceededRole(runs, task.Envelope.Role) != nil, nil)
	result.add("test_run_succeeded", latestSucceededRole(runs, "test") != nil, nil)
	result.add("audit_run_succeeded", latestSucceededRole(runs, "audit") != nil, nil)

	scope, err := st.LatestScopeCheck(task.ID)
	result.add("scope_check_passed", err == nil && scope.Result.Status == "passed", scope)

	approvals := st.ListApprovals(task.ID)
	result.add("pr_prepare_approved", hasApprovedPilotApproval(approvals, "pr_prepare"), nil)
	result.add("pr_publish_approved", hasApprovedPilotApproval(approvals, "pr_publish"), nil)

	prepared := latestGitSyncStatus(runs, "prepared")
	result.add("git_prepare_pr_completed", prepared != nil && prepared.Result["auto_merge"] == false, preparedResultDetail(prepared))
	if prepared != nil {
		result.add("git_prepare_publish_plan_safe", safePublishPlan(prepared.Result), prepared.Result["pr_publish_plan"])
	}

	dryRun := latestGitSyncStatus(runs, "publish_prepared")
	if dryRun != nil {
		result.DryRunID = dryRun.ID
	}
	result.add("dry_run_publish_prepared", dryRun != nil && publishFlag(dryRun.Result, "dry_run") == true && publishFlag(dryRun.Result, "auto_merge") == false, publishResultDetail(dryRun))

	live := latestGitSyncStatus(runs, "published")
	if live != nil {
		result.LiveRunID = live.ID
		result.PRURL = publishString(live.Result, "pr_url")
	}
	result.add("live_pr_published", live != nil && publishFlag(live.Result, "dry_run") == false && publishFlag(live.Result, "auto_merge") == false && publishFlag(live.Result, "credential_resolved") == true && result.PRURL != "", publishResultDetail(live))

	result.add("audit_task_create_recorded", hasAudit(st.ListAuditLogs(), "task", task.ID, "task_create", nil), nil)
	result.add("audit_git_prepare_recorded", prepared != nil && hasAudit(st.ListAuditLogs(), "run", prepared.ID, "git_prepare_pr", nil), nil)
	result.add("audit_dry_run_publish_recorded", dryRun != nil && hasAudit(st.ListAuditLogs(), "run", dryRun.ID, "git_publish_pr", func(payload map[string]any) bool {
		return payload["dry_run"] == true && payload["auto_merge"] == false
	}), nil)
	result.add("audit_live_publish_recorded", live != nil && hasAudit(st.ListAuditLogs(), "run", live.ID, "git_publish_pr", func(payload map[string]any) bool {
		return payload["dry_run"] == false && payload["credential_resolved"] == true && payload["auto_merge"] == false
	}), nil)

	if opts.Strict {
		result.add("audit_ship_receipt_present", jsonFileHasAny(opts.AuditShipReceipt, "status", "destination", "remote_status"), opts.AuditShipReceipt)
		result.add("backup_manifest_present", jsonFileHasAny(opts.BackupManifest, "created_at", "database_dump", "run_archive"), opts.BackupManifest)
		result.add("restore_evidence_present", jsonFileHasAny(opts.RestoreEvidence, "restored_at", "database_restore", "valid"), opts.RestoreEvidence)
		result.add("pilot_signoff_present", nonEmptyFile(opts.Signoff), opts.Signoff)
	}
	return result
}

func (r *pilotVerification) add(name string, ok bool, detail any) {
	status := "passed"
	if !ok {
		status = "failed"
		r.Valid = false
		r.Failures = append(r.Failures, name)
	}
	r.Checks = append(r.Checks, pilotVerifyCheck{Name: name, Status: status, Detail: detail})
}

func latestSucceededRole(runs []domain.Run, role string) *domain.Run {
	role = normalizePilotRole(role)
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizePilotRole(runs[i].Role) == role && runs[i].Status == "succeeded" {
			return &runs[i]
		}
	}
	return nil
}

func latestGitSyncStatus(runs []domain.Run, status string) *domain.Run {
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizePilotRole(runs[i].Role) != "git_sync" || runs[i].Status != "succeeded" {
			continue
		}
		if runStatus, _ := runs[i].Result["status"].(string); runStatus == status {
			return &runs[i]
		}
	}
	return nil
}

func normalizePilotRole(role string) string {
	if role == "git-sync" {
		return "git_sync"
	}
	return role
}

func hasApprovedPilotApproval(approvals []domain.Approval, approvalType string) bool {
	for _, approval := range approvals {
		if approval.ApprovalType == approvalType && approval.Status == "approved" {
			return true
		}
	}
	return false
}

func safePublishPlan(result map[string]any) bool {
	plan := asMap(result["pr_publish_plan"])
	return plan["required_approval"] == "pr_publish" && plan["auto_merge"] == false
}

func publishFlag(result map[string]any, key string) bool {
	if value, ok := result[key].(bool); ok {
		return value
	}
	publish := asMap(result["publish_result"])
	value, _ := publish[key].(bool)
	return value
}

func publishString(result map[string]any, key string) string {
	if value, ok := result[key].(string); ok {
		return value
	}
	publish := asMap(result["publish_result"])
	value, _ := publish[key].(string)
	return value
}

func publishResultDetail(run *domain.Run) any {
	if run == nil {
		return nil
	}
	return map[string]any{
		"run_id":              run.ID,
		"status":              run.Result["status"],
		"dry_run":             publishFlag(run.Result, "dry_run"),
		"auto_merge":          publishFlag(run.Result, "auto_merge"),
		"credential_resolved": publishFlag(run.Result, "credential_resolved"),
		"pr_url":              publishString(run.Result, "pr_url"),
	}
}

func preparedResultDetail(run *domain.Run) any {
	if run == nil {
		return nil
	}
	return map[string]any{
		"run_id":     run.ID,
		"status":     run.Result["status"],
		"auto_merge": run.Result["auto_merge"],
		"plan":       run.Result["pr_publish_plan"],
	}
}

func asMap(value any) map[string]any {
	if value == nil {
		return nil
	}
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var mapped map[string]any
	if err := json.Unmarshal(data, &mapped); err != nil {
		return nil
	}
	return mapped
}

func hasAudit(logs []domain.AuditLog, resourceType string, resourceID string, actionSuffix string, match func(map[string]any) bool) bool {
	for _, entry := range logs {
		if entry.ResourceType != resourceType || entry.ResourceID != resourceID || !strings.HasSuffix(entry.Action, actionSuffix) {
			continue
		}
		if match == nil || match(entry.Payload) {
			return true
		}
	}
	return false
}

func jsonFileHasAny(path string, keys ...string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return false
	}
	for _, key := range keys {
		if _, ok := decoded[key]; ok {
			return true
		}
	}
	return false
}

func nonEmptyFile(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

func (r pilotVerification) String() string {
	return fmt.Sprintf("pilot verification task=%s valid=%v", r.TaskID, r.Valid)
}
