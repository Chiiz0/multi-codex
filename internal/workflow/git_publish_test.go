package workflow

import (
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestRenderPRPublishPlanRequiresApprovalAndNoAutoMerge(t *testing.T) {
	task := domain.Task{
		Title: "Ship scoped change",
		Envelope: domain.TaskEnvelope{
			BaseBranch:   "main",
			TargetBranch: "codex/ship-scoped-change",
		},
	}
	repo := domain.Repository{Provider: "github", RemoteURL: "https://github.com/example/repo.git"}

	plan := RenderPRPublishPlan(task, repo, "artifact-1")
	if plan["required_approval"] != "pr_publish" {
		t.Fatalf("required approval = %#v", plan["required_approval"])
	}
	if plan["auto_merge"] != false {
		t.Fatalf("auto_merge = %#v", plan["auto_merge"])
	}
	if plan["credential_required"] != "GITHUB_TOKEN" {
		t.Fatalf("credential_required = %#v", plan["credential_required"])
	}
}
