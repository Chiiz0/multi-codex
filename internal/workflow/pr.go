package workflow

import (
	"strings"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func RenderPRBody(task domain.Task, state domain.WorkflowState) string {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(task.Title)
	b.WriteString("\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString("- Task: `")
	b.WriteString(task.TaskKey)
	b.WriteString("`\n")
	b.WriteString("- Project: `")
	b.WriteString(task.ProjectID)
	b.WriteString("`\n")
	b.WriteString("- Repository: `")
	b.WriteString(task.RepositoryID)
	b.WriteString("`\n")
	b.WriteString("- Target branch: `")
	b.WriteString(task.Envelope.TargetBranch)
	b.WriteString("`\n\n")

	b.WriteString("## Objective\n\n")
	if task.Envelope.Objective == "" {
		b.WriteString("No objective was provided.")
	} else {
		b.WriteString(task.Envelope.Objective)
	}
	b.WriteString("\n\n")

	b.WriteString("## Acceptance Criteria\n\n")
	if len(task.Envelope.AcceptanceCriteria) == 0 {
		b.WriteString("- No acceptance criteria were provided.\n")
	} else {
		for _, item := range task.Envelope.AcceptanceCriteria {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Gate Results\n\n")
	if state.LatestScopeCheck != nil {
		b.WriteString("- Scope: `")
		b.WriteString(state.LatestScopeCheck.Result.Status)
		b.WriteString("`")
		if len(state.LatestScopeCheck.Result.ChangedFiles) > 0 {
			b.WriteString(" (")
			b.WriteString(strings.Join(state.LatestScopeCheck.Result.ChangedFiles, ", "))
			b.WriteString(")")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("- Scope: `not_recorded`\n")
	}
	b.WriteString("- Test: `")
	b.WriteString(LatestRunStatus(state.Runs, "test"))
	b.WriteString("`\n")
	b.WriteString("- Audit: `")
	b.WriteString(LatestRunStatus(state.Runs, "audit"))
	b.WriteString("`\n")
	b.WriteString("- Human approval: `")
	b.WriteString(ApprovalStatus(state.Approvals, "pr_prepare"))
	b.WriteString("`\n\n")

	b.WriteString("## Safety Notes\n\n")
	b.WriteString("- Git Sync prepared this PR body only.\n")
	b.WriteString("- No push or merge is performed automatically.\n")
	b.WriteString("- Human review remains required before any remote operation.\n")
	return b.String()
}

func LatestRunStatus(runs []domain.Run, role string) string {
	for i := len(runs) - 1; i >= 0; i-- {
		if normalizeRole(runs[i].Role) == normalizeRole(role) {
			return runs[i].Status
		}
	}
	return "not_run"
}

func ApprovalStatus(approvals []domain.Approval, approvalType string) string {
	for _, approval := range approvals {
		if approval.ApprovalType == approvalType {
			return approval.Status
		}
	}
	return "not_requested"
}
