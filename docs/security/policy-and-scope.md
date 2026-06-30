# Policy and Scope

`multi-codex` treats Skills as work instructions, not security boundaries. The hard boundaries are:

- Task Envelope validation.
- Gateway tool allowlist.
- Executor isolation.
- Independent worktrees.
- Git diff based scope checks.
- Test and audit gates.
- Human approval before push or merge.

## Current Scope Check

The first implementation supports glob-like patterns:

- `*` matches one path segment.
- `**` matches across path separators.

Scope check result:

- `passed`: every changed file matches `allowed_paths` and avoids `forbidden_paths`.
- `blocked`: at least one changed file is outside scope or forbidden.

Executor-side scope checks record `scope_check` run events and
`worker.scope_check` audit rows. A blocked result updates the task to
`blocked` and prevents later Git Sync gates from becoming ready.

## Command Policy

`TaskEnvelope.allowed_commands` is now checked before worker execution. The
deployment can configure:

```bash
MULTICODEX_WORKER_COMMAND_ALLOWLIST="go test;pnpm --dir apps/web build"
MULTICODEX_WORKER_COMMAND_DENYLIST="docker;git push;kubectl;terraform apply"
```

The denylist is active by default for dangerous host, cluster, Docker, SSH,
push, privilege, and cloud metadata patterns. The allowlist is optional; when it
is set, every allowed command in the Task Envelope must match it. Decisions are
recorded as `worker_command_policy` run events and `worker.command_policy`
audit rows. A blocked command policy finishes the run as `blocked`.

## Dependency And Lockfile Policy

When `TaskEnvelope.policy.allow_dependency_change=false`, worker collection
checks changed files for dependency manifests and lockfiles such as `go.mod`,
`go.sum`, `package.json`, `pnpm-lock.yaml`, `Cargo.lock`, `pyproject.toml`,
`uv.lock`, and `Gemfile.lock`.

Decisions are recorded as `dependency_policy` run events and
`worker.dependency_policy` audit rows. Dependency or lockfile changes are still
allowed only when the Task Envelope policy explicitly allows them.

## High-Risk Defaults

High-risk paths are listed in `internal/policy/validator.go` and mirrored in `configs/multi-codex.example.yaml`. They should later become organization or project policy records.

## Resource-Scoped Authorization

API and MCP authorization is both role-aware and resource-aware:

- A user's membership `org_id` is compared against project, Skill, executor node, and audit log `org_id`.
- Repositories, tasks, runs, artifacts, approvals, and agent profiles inherit their organization boundary through their parent project or task.
- Cross-organization reads and mutations return `403` and record `api.authorization_denied` or `mcp.authorization_denied` with `trace_id`, actor org, resource org, and requested action/tool.
- List endpoints filter projects, Skills, executor nodes, approvals, runs, and audit logs to the caller's organization.
- Task Envelope validation rejects a `repository_id` that belongs to a different `project_id`.
- In OIDC multi-organization mode, manual queue dispatch is disabled until the store supports dispatching a specific queued run inside the caller's organization. The background scheduler can still dispatch queued work.

RBAC still controls the action class first. Resource checks then narrow access to the caller's organization even when the role grants the coarse permission.

## Required Follow-Ups

- Keep scheduled audit seal/ship enabled for production append-only WORM/SIEM retention.

## Related Security Docs

- [Enterprise Hardening](enterprise-hardening.md)
