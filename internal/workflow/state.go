package workflow

import (
	"errors"

	"github.com/Chiiz0/multi-codex/internal/domain"
	"github.com/Chiiz0/multi-codex/internal/store"
)

func BuildState(st store.Store, task domain.Task) domain.WorkflowState {
	runs := st.ListRuns(task.ID)
	approvals := st.ListApprovals(task.ID)
	if runs == nil {
		runs = []domain.Run{}
	}
	if approvals == nil {
		approvals = []domain.Approval{}
	}
	state := domain.WorkflowState{
		Task:           task,
		Runs:           runs,
		Approvals:      approvals,
		BlockedReasons: []string{},
		NextActions:    []string{},
	}
	if scope, err := st.LatestScopeCheck(task.ID); err == nil {
		state.LatestScopeCheck = &scope
		if scope.Result.Status == "blocked" {
			state.BlockedReasons = append(state.BlockedReasons, "scope violation blocks test, audit, and git sync")
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		state.BlockedReasons = append(state.BlockedReasons, "scope check state could not be loaded")
	}

	if hasRejectedApproval(approvals) {
		state.BlockedReasons = append(state.BlockedReasons, "approval rejected")
	}
	if failedRun := latestTerminalRun(runs, "test", "failed", "blocked", "timed_out", "cancelled"); failedRun != nil {
		state.BlockedReasons = append(state.BlockedReasons, "test failed or was blocked")
	}
	if failedRun := latestTerminalRun(runs, "audit", "failed", "blocked", "timed_out", "cancelled"); failedRun != nil {
		state.BlockedReasons = append(state.BlockedReasons, "audit blocker present")
	}
	if failedRun := latestTerminalRun(runs, "git_sync", "failed", "blocked", "timed_out", "cancelled"); failedRun != nil {
		state.BlockedReasons = append(state.BlockedReasons, "git sync failed or was blocked")
	}

	if len(state.BlockedReasons) > 0 {
		state.NextActions = append(state.NextActions, "resolve_blocker")
		return state
	}

	feature := latestRun(runs, normalizeRole(task.Envelope.Role))
	if task.Status == "draft" {
		state.NextActions = append(state.NextActions, "policy_validate_task")
	}
	if feature == nil {
		state.NextActions = append(state.NextActions, "worker_spawn")
		return state
	}
	if !succeeded(feature) {
		state.NextActions = append(state.NextActions, "worker_status")
		return state
	}
	if state.LatestScopeCheck == nil {
		state.NextActions = append(state.NextActions, "repo_scope_check")
		return state
	}
	if state.LatestScopeCheck.Result.Status != "passed" {
		state.BlockedReasons = append(state.BlockedReasons, "scope check did not pass")
		state.NextActions = append(state.NextActions, "resolve_blocker")
		return state
	}

	if task.Envelope.Policy.RequireTests && latestSucceededRun(runs, "test") == nil {
		state.NextActions = append(state.NextActions, "test_run_required")
		return state
	}
	if task.Envelope.Policy.RequireAudit && latestSucceededRun(runs, "audit") == nil {
		state.NextActions = append(state.NextActions, "audit_run")
		return state
	}
	if task.Envelope.Policy.RequireHumanBeforePR && !hasApprovedApproval(approvals, "pr_prepare") {
		if hasPendingApproval(approvals, "pr_prepare") {
			state.NextActions = append(state.NextActions, "approval_status")
		} else {
			state.NextActions = append(state.NextActions, "approval_request")
		}
		return state
	}

	state.ReadyForPR = true
	preparedPR := latestRunWithResultStatus(runs, "git_sync", "prepared")
	publishedPR := latestRunWithResultStatus(runs, "git_sync", "published")
	if preparedPR == nil {
		state.NextActions = append(state.NextActions, "git_prepare_pr")
	} else if !hasApprovedApproval(approvals, "pr_publish") {
		if hasPendingApproval(approvals, "pr_publish") {
			state.NextActions = append(state.NextActions, "approval_status")
		} else {
			state.NextActions = append(state.NextActions, "approval_request_pr_publish")
		}
	} else if publishedPR == nil {
		state.NextActions = append(state.NextActions, "git_publish_pr")
	} else {
		state.NextActions = append(state.NextActions, "completed")
	}
	return state
}

func GateAllows(state domain.WorkflowState, action string) (bool, []string) {
	if len(state.BlockedReasons) > 0 {
		return false, state.BlockedReasons
	}
	for _, next := range state.NextActions {
		if next == action {
			return true, nil
		}
	}
	return false, []string{"workflow state does not allow " + action}
}

func latestRun(runs []domain.Run, role string) *domain.Run {
	role = normalizeRole(role)
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizeRole(runs[i].Role) == role {
			return &runs[i]
		}
	}
	return nil
}

func latestSucceededRun(runs []domain.Run, role string) *domain.Run {
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizeRole(runs[i].Role) == normalizeRole(role) && succeeded(&runs[i]) {
			return &runs[i]
		}
	}
	return nil
}

func latestRunWithResultStatus(runs []domain.Run, role string, statuses ...string) *domain.Run {
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizeRole(runs[i].Role) != normalizeRole(role) || !succeeded(&runs[i]) {
			continue
		}
		runStatus, _ := runs[i].Result["status"].(string)
		for _, status := range statuses {
			if runStatus == status {
				return &runs[i]
			}
		}
	}
	return nil
}

func LatestRunWithResultStatus(runs []domain.Run, role string, statuses ...string) *domain.Run {
	return latestRunWithResultStatus(runs, role, statuses...)
}

func latestTerminalRun(runs []domain.Run, role string, statuses ...string) *domain.Run {
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizeRole(runs[i].Role) != normalizeRole(role) {
			continue
		}
		for _, status := range statuses {
			if runs[i].Status == status {
				return &runs[i]
			}
		}
		return nil
	}
	return nil
}

func succeeded(run *domain.Run) bool {
	return run != nil && run.Status == "succeeded"
}

func hasApprovedApproval(approvals []domain.Approval, approvalType string) bool {
	for _, approval := range approvals {
		if approval.ApprovalType == approvalType && approval.Status == "approved" {
			return true
		}
	}
	return false
}

func hasPendingApproval(approvals []domain.Approval, approvalType string) bool {
	for _, approval := range approvals {
		if approval.ApprovalType == approvalType && approval.Status == "pending" {
			return true
		}
	}
	return false
}

func hasRejectedApproval(approvals []domain.Approval) bool {
	for _, approval := range approvals {
		if approval.Status == "rejected" {
			return true
		}
	}
	return false
}

func normalizeRole(role string) string {
	if role == "git-sync" {
		return "git_sync"
	}
	return role
}
