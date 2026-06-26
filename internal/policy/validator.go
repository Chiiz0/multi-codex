package policy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/Chiiz0/multi-codex/internal/domain"
)

var DefaultForbiddenPaths = []string{
	".github/**",
	".gitlab/**",
	"infra/**",
	"k8s/**",
	"terraform/**",
	"secrets/**",
	".env*",
	"**/*secret*",
	"**/*credential*",
	"package-lock.json",
	"pnpm-lock.yaml",
	"go.sum",
}

var allowedRoles = []string{"main", "feature", "test", "audit", "git-sync", "git_sync", "docs", "release"}
var allowedExecutors = []string{"docker", "ssh"}

func ValidateTaskEnvelope(envelope domain.TaskEnvelope) domain.ValidationResult {
	result := domain.ValidationResult{Valid: true}

	requireString(&result, "task_id", envelope.TaskID)
	requireString(&result, "project_id", envelope.ProjectID)
	requireString(&result, "repository_id", envelope.RepositoryID)
	requireString(&result, "title", envelope.Title)
	requireString(&result, "role", envelope.Role)
	requireString(&result, "executor", envelope.Executor)

	if len(envelope.AllowedPaths) == 0 {
		result.Errors = append(result.Errors, "allowed_paths must not be empty")
	}
	if len(envelope.ForbiddenPaths) == 0 {
		result.Errors = append(result.Errors, "forbidden_paths must not be empty")
	}
	if envelope.Role != "" && !slices.Contains(allowedRoles, envelope.Role) {
		result.Errors = append(result.Errors, fmt.Sprintf("role %q is not supported", envelope.Role))
	}
	if envelope.Executor != "" && !slices.Contains(allowedExecutors, envelope.Executor) {
		result.Errors = append(result.Errors, fmt.Sprintf("executor %q is not supported", envelope.Executor))
	}
	if protectedBranch(envelope.TargetBranch) {
		result.Errors = append(result.Errors, "target_branch must not point at a protected branch")
	}
	if envelope.Network {
		result.Warnings = append(result.Warnings, "network=true requires human approval before worker spawn")
	}
	if !envelope.Policy.RequireAudit {
		result.Warnings = append(result.Warnings, "policy.require_audit is false; MVP flow expects audit by default")
	}
	if !envelope.Policy.RequireTests {
		result.Warnings = append(result.Warnings, "policy.require_tests is false; MVP flow expects tests by default")
	}

	missingSensitive := missingDefaultForbidden(envelope.ForbiddenPaths)
	if len(missingSensitive) > 0 {
		result.Warnings = append(result.Warnings, "forbidden_paths should include default sensitive patterns: "+strings.Join(missingSensitive, ", "))
	}

	result.Valid = len(result.Errors) == 0
	return result
}

func requireString(result *domain.ValidationResult, field, value string) {
	if strings.TrimSpace(value) == "" {
		result.Errors = append(result.Errors, field+" is required")
	}
}

func protectedBranch(branch string) bool {
	normalized := strings.TrimPrefix(strings.TrimSpace(branch), "origin/")
	return normalized == "main" || normalized == "master" || normalized == "trunk" || normalized == "develop"
}

func missingDefaultForbidden(paths []string) []string {
	var missing []string
	for _, required := range DefaultForbiddenPaths {
		if !slices.Contains(paths, required) {
			missing = append(missing, required)
		}
	}
	return missing
}
