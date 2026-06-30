# Production Readiness Plan

This plan turns the current enterprise MVP into an operator-ready release. It is based on the current repository state and the 2026-06-26 verification report. Before starting implementation work, refresh verification from the fixed dev image because host-local Go/Node tooling is not part of the supported workflow.

## Current Baseline

The MVP already has the core product surface:

- API, MCP Gateway, Web Console, PostgreSQL migrations, and Docker Compose.
- Organizations, projects, repositories, tasks, runs, events, artifacts, approvals, Skills, Agent Profiles, executor nodes, queueing, and audit logs.
- Docker and SSH executor paths with run artifacts, worker timeouts, queue backpressure, and basic scheduler fairness.
- OIDC bearer validation, authorization-code login with PKCE, HttpOnly browser sessions, logout, revocation, and back-channel logout.
- Metrics, audit hash-chain verification, audit seal/ship, retention cleanup, backup/restore, and local production-style Compose.

The remaining work is less about adding broad feature area and more about making the system safe to operate with real users, real repositories, real credentials, and a repeatable release process.

## Release Target

The first online version should be a controlled enterprise deployment for a small set of internal teams:

- OIDC-only user authentication with secure browser cookies.
- Resource-scoped API and MCP authorization across organizations, projects, repositories, tasks, runs, artifacts, approvals, executor nodes, and audit records.
- Isolated worker execution with explicit secrets, network, resource limits, and audit trails.
- Reproducible container images and migration flow.
- Backup, restore, retention, audit export, telemetry, alerting, and incident runbooks.
- A pilot path that starts with dry-run PR publishing, then enables live PR creation without auto-merge.

## Milestones

### M0: Refresh the Truth

Goal: prove the current branch is reproducible before adding production hardening.

Required work:

- Build the fixed dev image and worker image from the repository.
- Re-run backend tests, backend builds, frontend production build, compose config validation, migration smoke, and API/MCP health checks.
- Refresh `docs/operations/verification-report.md` with the new date, command outputs, and any failures.
- Reconcile stale documentation against implemented behavior, especially security follow-ups that have changed since the MVP report.

Exit criteria:

- `make dev-image`
- `make worker-image`
- `make backend-test`
- `make backend-build`
- `make frontend-build`
- `make compose-config`
- `make migrate-dev`
- API, MCP Gateway, Web Console, and worker-agentd health checks pass from Compose.

### M1: Fail-Closed Production Configuration

Goal: prevent unsafe local defaults from being used in an online deployment.

Required work:

- Add an explicit production mode or readiness validator that rejects unsafe combinations before serving traffic.
- Require OIDC mode, configured issuer/audience/JWKS, secure cookies, non-empty database password, agentd token when agentd is exposed, and explicit audit/retention policy.
- Restrict CORS to configured origins instead of echoing arbitrary origins in production.
- Add production `.env` template documentation without secrets.
- Add tests for safe local defaults and fail-closed production misconfiguration.

Exit criteria:

- The API and MCP Gateway refuse production mode with local auth, insecure cookies, missing OIDC config, or open CORS.
- Local development remains unchanged with `.env.example` and dev Compose.

### M2: Resource-Scoped Authorization

Goal: make multi-organization use safe.

Required work:

- Replace coarse path-permission checks with route and resource-aware authorization.
- Ensure every API list/get/mutate endpoint filters or denies by the authenticated user's organization and role.
- Add MCP tool permission checks that mirror API permissions.
- Add cross-organization denial tests for projects, repositories, tasks, runs, artifacts, approvals, executor nodes, Skills, Agent Profiles, audit logs, and MCP tools.
- Make the Web Console hide or disable actions that the current user cannot perform.

Exit criteria:

- A viewer cannot mutate resources.
- A member of one organization cannot read or act on another organization's resources through either API or MCP.
- Audit rows record denied authorization decisions with trace IDs.

### M3: Worker Isolation and Execution Controls

Goal: run real Codex workers without giving the control plane unnecessary host power.

Required work:

- Move production Docker execution away from mounting the host Docker socket into the API container, or document and enforce a dedicated isolated worker host boundary.
- Enforce worker CPU, memory, process, filesystem, timeout, and network limits from policy.
- Add command allowlist or denied-command detection for worker execution plans.
- Add dependency and lockfile policy checks when `allow_dependency_change=false`.
- Expand secret redaction from keyword matching to structured token detectors plus configured secret-value redaction.
- Define SSH key distribution, host key rotation, agentd token rotation, and network segmentation runbooks.

Exit criteria:

- A production worker can run a scoped task with real credentials and network access only when task, profile, and deployment policy all allow it.
- Scope, dependency, secret, timeout, and network decisions are visible in run events and audit logs.

### M4: Release and Deployment Packaging

Goal: make the system deployable by operators without hand assembly.

Required work:

- Add CI that runs backend tests, frontend build, compose config validation, image build, and migration checks.
- Publish immutable image tags for API, Web, and worker images.
- Add SBOM and vulnerability scan outputs for release artifacts.
- Add a migration job pattern for production deploys.
- Harden production Compose and add a Kubernetes or Helm deployment path when the first target environment requires it.
- Add TLS/reverse-proxy guidance for API, MCP Gateway, Web Console, and worker-agentd.

Exit criteria:

- A tagged release can be built, scanned, and deployed from documented commands.
- Rollback steps are documented for app images and database migrations.

### M5: Operations, Observability, and DR

Goal: make failure boring.

Required work:

- Add dashboards and alert rules for API/MCP latency, error rate, queue depth, worker failures, audit ship failures, retention failures, DB health, and disk usage.
- Run backup and restore drills against a disposable environment.
- Enable scheduled audit seal/ship to append-only object storage or SIEM.
- Move retention from dry-run to delete mode only after documented approval.
- Write incident runbooks for OIDC outage, database outage, stuck queue, runaway worker, failed audit ship, and compromised worker credential.

Exit criteria:

- Operators can detect, triage, and recover the most likely production failures without reading code.
- RPO/RTO targets are documented and proven by restore drill evidence.

### M6: Product Pilot

Goal: validate the system on real repositories without overexposing blast radius.

Required work:

- Select one low-risk internal repository and one team.
- Provision OIDC groups, organization, project, repository, Skills, Agent Profiles, and executor nodes.
- Run feature, test, audit, approval, and Git Sync in dry-run mode.
- Enable live PR creation only after dry-run audit evidence is reviewed.
- Keep auto-merge disabled.
- Capture pilot findings as issues or focused docs, not as one growing notes file.

Exit criteria:

- The pilot creates a reviewable PR from a governed workflow.
- Rollback, audit export, and restore paths have been exercised at least once.
- The team signs off on security, operations, and product usability for broader rollout.

## Immediate Next Sprint

Start with these items in order:

1. Build the fixed dev image and refresh the full verification report.
2. Add production-mode readiness validation and tests.
3. Implement MCP tool-level permission checks.
4. Start resource-scoped API authorization with project, repository, task, run, artifact, and approval endpoints.
5. Document the production deployment checklist and the first pilot runbook.

This sequence intentionally protects the release path before expanding functionality.
