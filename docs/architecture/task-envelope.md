# Task Envelope

The Task Envelope is the per-task contract used by the Gateway, Executor, and Worker.

## Required MVP Fields

- `task_id`
- `project_id`
- `repository_id`
- `title`
- `role`
- `executor`
- `allowed_paths`
- `forbidden_paths`

## Policy Expectations

`allowed_paths` must be narrow enough for a role-specific worker. `forbidden_paths` should include the default sensitive patterns:

```text
.github/**
.gitlab/**
infra/**
k8s/**
terraform/**
secrets/**
.env*
**/*secret*
**/*credential*
package-lock.json
pnpm-lock.yaml
go.sum
```

The current validator blocks missing hard requirements and warns when sensitive defaults are missing. Later phases will move this into project-level policy configuration and stored validation records.

## Example

```json
{
  "task_id": "FEAT-123",
  "project_id": "00000000-0000-7000-8000-000000000101",
  "repository_id": "00000000-0000-7000-8000-000000000201",
  "title": "Add scope check API",
  "base_branch": "origin/main",
  "target_branch": "codex/feat-123/scope-check",
  "role": "feature",
  "skill": "company-feature-worker",
  "agent_profile": "feature-worker-go-node",
  "executor": "docker",
  "allowed_paths": ["internal/policy/**", "internal/api/**"],
  "forbidden_paths": [".github/**", "infra/**", "secrets/**", ".env*", "go.sum"],
  "network": false,
  "objective": "Expose scope check result through the API.",
  "acceptance_criteria": ["Unit tests cover allowed and forbidden paths."],
  "policy": {
    "allow_push": false,
    "allow_dependency_change": false,
    "allow_infra_change": false,
    "require_audit": true,
    "require_tests": true,
    "require_human_before_pr": true
  }
}
```
