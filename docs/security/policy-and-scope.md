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

## High-Risk Defaults

High-risk paths are listed in `internal/policy/validator.go` and mirrored in `configs/multi-codex.example.yaml`. They should later become organization or project policy records.

## Required Follow-Ups

- Add command allowlist execution enforcement inside workers.
- Add dependency and lockfile policy checks beyond path-level blocking.
- Expand secret redaction from keyword redaction to structured detector rules.
- Add production RBAC middleware and OIDC identity binding.
- Add immutable audit log storage option.

## Related Security Docs

- [Enterprise Hardening](enterprise-hardening.md)
