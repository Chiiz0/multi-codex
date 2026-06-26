package policy

import (
	"testing"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

func TestValidateTaskEnvelopeRequiresScope(t *testing.T) {
	result := ValidateTaskEnvelope(domain.TaskEnvelope{
		TaskID:       "FEAT-1",
		ProjectID:    "proj_demo",
		RepositoryID: "repo_demo",
		Title:        "Add scope check",
		Role:         "feature",
		Executor:     "docker",
		TargetBranch: "codex/feat-1/scope-check",
	})

	if result.Valid {
		t.Fatalf("expected invalid envelope without allowed_paths and forbidden_paths")
	}
}

func TestValidateTaskEnvelopeBlocksProtectedTargetBranch(t *testing.T) {
	result := ValidateTaskEnvelope(validEnvelope(func(envelope *domain.TaskEnvelope) {
		envelope.TargetBranch = "origin/main"
	}))

	if result.Valid {
		t.Fatalf("expected protected target branch to be invalid")
	}
}

func TestCheckScopeBlocksOutsideAllowedPaths(t *testing.T) {
	result := CheckScope(
		[]string{"internal/policy/scope.go", "deployments/docker/compose.yaml"},
		[]string{"internal/policy/**"},
		[]string{"deployments/**"},
	)

	if result.Status != "blocked" {
		t.Fatalf("expected blocked status, got %q", result.Status)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected one violation, got %d: %#v", len(result.Violations), result.Violations)
	}
}

func TestCheckScopePassesAllowedFiles(t *testing.T) {
	result := CheckScope(
		[]string{"internal/policy/scope.go", "internal/policy/scope_test.go"},
		[]string{"internal/policy/**"},
		[]string{"deployments/**"},
	)

	if result.Status != "passed" {
		t.Fatalf("expected passed status, got %q with violations %#v", result.Status, result.Violations)
	}
}

func validEnvelope(mutators ...func(*domain.TaskEnvelope)) domain.TaskEnvelope {
	envelope := domain.TaskEnvelope{
		TaskID:         "FEAT-1",
		ProjectID:      "proj_demo",
		RepositoryID:   "repo_demo",
		Title:          "Add scope check",
		Role:           "feature",
		Executor:       "docker",
		TargetBranch:   "codex/feat-1/scope-check",
		AllowedPaths:   []string{"internal/policy/**"},
		ForbiddenPaths: DefaultForbiddenPaths,
		Policy: domain.TaskPolicy{
			RequireAudit: true,
			RequireTests: true,
		},
	}
	for _, mutate := range mutators {
		mutate(&envelope)
	}
	return envelope
}
