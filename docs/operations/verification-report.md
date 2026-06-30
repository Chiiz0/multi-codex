# Verification Report

Last verified: 2026-06-30.

This report records the current reproducibility baseline for the production
readiness work. Commands were run from `C:\SparkClaw\multi-codex`.

## Environment

- Docker Engine: `29.5.2`
- Fixed dev image: `multi-codex/dev:go1.25-node25.9-pnpm11.7`
- Worker image: `multi-codex/codex-worker:go1.25-node-vite8`
- Codex CLI in worker image: `codex-cli 0.142.2`
- PostgreSQL image: `postgres:18`
- Compose file: `deployments/docker/compose.dev.yaml`

The Windows host does not have `make` installed, so the Makefile targets were
verified through their Docker-equivalent commands. Build and test commands ran
inside the fixed dev image.

## M0 Commands

| Check | Command | Result |
| --- | --- | --- |
| Docker available | `docker version --format '{{.Server.Version}}'` | passed, `29.5.2` |
| Dev image build | `docker build -f deployments/docker/Dockerfile.dev -t multi-codex/dev:go1.25-node25.9-pnpm11.7 .` | passed |
| Worker image build | `docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:go1.25-node-vite8 .` | passed |
| Backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| Backend build | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go build ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd ./cmd/mcxctl` | passed |
| Frontend build | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev bash -c 'pnpm --dir apps/web install --frozen-lockfile && pnpm --dir apps/web build'` | passed |
| Dev Compose config | `docker compose -f deployments/docker/compose.dev.yaml config` | passed |
| Production Compose config | `POSTGRES_PASSWORD=m0-temp-postgres-password-20260630 docker compose -f deployments/docker/compose.yaml config` | passed |
| Migration smoke | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go run ./cmd/mcxctl migrate` | passed |
| Compose smoke | `docker compose -f deployments/docker/compose.dev.yaml up -d --wait postgres api-dev mcp-gateway-dev worker-agentd-dev web-dev` | passed |

## Notable Output

Backend test packages all passed, including API, MCP, auth, executor, policy,
secrets, store, scheduler, retention, and workflow packages.

Frontend production build passed with:

```text
vite v8.1.0 building client environment for production...
✓ 130 modules transformed.
✓ built in 331ms
```

Migration smoke returned:

```json
{"level":"INFO","msg":"migrations applied","migrations":"internal/db/migrations"}
```

Compose health checks returned:

| Service | URL | Status |
| --- | --- | --- |
| API | `http://localhost:18080/healthz` | `200` |
| MCP Gateway | `http://localhost:18090/healthz` | `200` |
| Web Console | `http://localhost:13000` | `200` |
| worker-agentd | `http://localhost:17070/healthz` | `200` |

## Failures and Fixes

The first migration smoke attempt failed because another local container already
owned host port `5432`, so dev Compose could not start PostgreSQL. The dev
Compose file now allows host port overrides while preserving the original
defaults:

- `MULTICODEX_POSTGRES_HOST_PORT`
- `MULTICODEX_API_HOST_PORT`
- `MULTICODEX_MCP_HOST_PORT`
- `MULTICODEX_AGENTD_HOST_PORT`
- `MULTICODEX_WEB_HOST_PORT`

The M0 Compose smoke used:

```powershell
$env:MULTICODEX_POSTGRES_HOST_PORT='55432'
$env:MULTICODEX_API_HOST_PORT='18080'
$env:MULTICODEX_MCP_HOST_PORT='18090'
$env:MULTICODEX_AGENTD_HOST_PORT='17070'
$env:MULTICODEX_WEB_HOST_PORT='13000'
```

An initial manual test used `bash -lc` in the dev image, which reset `PATH` and
hid `/usr/local/go/bin/go`. The fixed image contains Go correctly; reproducible
commands should use the process directly or a non-login shell such as `bash -c`.

## Current Baseline

M0 exit criteria are satisfied through fixed-image equivalent commands:

- Fixed dev image builds.
- Worker image builds and contains `codex-cli 0.142.2`.
- Backend tests pass.
- Backend binaries build.
- Frontend production build passes.
- Dev and production Compose config validation passes.
- Migrations apply against PostgreSQL 18.
- API, MCP Gateway, Web Console, and worker-agentd start from Compose and pass
  smoke health checks.

## M1 Production Configuration Checks

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Config/API tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/config ./internal/api` | passed |
| Entrypoint tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd` | passed |
| API fail-closed smoke | `MULTICODEX_ENV=production MULTICODEX_AUTH_MODE=local go run ./cmd/api` in the fixed dev image | expected failure, exit `1` |
| MCP fail-closed smoke | `MULTICODEX_ENV=production MULTICODEX_AUTH_MODE=local go run ./cmd/mcp-gateway` in the fixed dev image | expected failure, exit `1` |
| worker-agentd fail-closed smoke | `MULTICODEX_ENV=production MULTICODEX_AGENTD_LISTEN=:7070 MULTICODEX_AGENTD_TOKEN= go run ./cmd/worker-agentd` in the fixed dev image | expected failure, exit `1` |
| Compose config | dev and production Compose `config` | passed |

The API refused production mode with local auth, missing OIDC settings,
insecure cookies, missing database password, open CORS, missing agentd token,
and missing audit/retention policy. MCP Gateway refused production mode with
local auth, missing OIDC settings, missing database password, and missing
agentd token. worker-agentd refused production mode when exposed without a
Bearer token.

## M2 Resource Authorization Checks

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| API/MCP/policy/store resource tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/api ./internal/mcp ./internal/policy ./internal/store` | passed |

Coverage added in this pass:

- API denies cross-organization access to projects, repositories, agent
  profiles, tasks, task runs, runs, run artifacts, artifact content, approvals,
  executor node host-key verification, and Skill version history.
- API list endpoints filter projects, approvals, executor nodes, Skills, and
  audit logs to the caller's organization.
- OIDC viewer users cannot mutate resources.
- MCP denies cross-organization `task_list` and `task_get` tools.
- MCP viewer contexts cannot call mutating `task_create`.
- MCP `organization_list` returns only the caller's current organization.
- Denied API and MCP authorization decisions are audited with trace IDs.

## M3 Worker Isolation And Execution Controls

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Worker/config/policy tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/config ./internal/policy ./internal/executor` | passed |
| Full backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| Compose config | dev and production Compose `config` | passed |

Coverage added in this pass:

- Production validation rejects Docker executor mode unless the Docker socket is
  explicitly enabled with `isolated-worker-host` boundary.
- Production Compose no longer mounts `/var/run/docker.sock` by default.
- Docker worker args enforce CPU, memory, pids, read-only root filesystem,
  tmpfs, no-new-privileges, and cap-drop settings.
- Worker command policy blocks denied commands and optionally enforces a
  deployment allowlist.
- Dependency and lockfile policy blocks changed dependency files when
  `allow_dependency_change=false`.
- Worker scope, command, dependency, resource, network, timeout, and secret
  decisions are visible in run events and audit rows.
- Executor redaction detects configured secret values plus common structured
  token formats before persisting logs, result files, diffs, and Docker output.

## M4 Release And Deployment Packaging

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Full backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| API image build | `docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:m4-verify .` | passed |
| Web image build | `docker build -f deployments/docker/Dockerfile.web -t multi-codex/web:m4-verify .` | passed |
| Worker image build | `docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:m4-verify .` | passed |
| Migration profile config | `POSTGRES_PASSWORD=m4-temp-postgres-password-20260630 docker compose -f deployments/docker/compose.yaml --profile migrate config` | passed |
| Image tool check | `docker run --rm --entrypoint /usr/local/bin/mcxctl multi-codex/api:m4-verify` | printed `mcxctl` usage, proving the migration CLI is present |

Built image IDs:

- API: `sha256:3b85cb3a0cb8ba1bc4447f3d586a2fa2aa2533023413653180bd1c397dd63c39`
- Web: `sha256:75430e43f82ccc02e25fbd2b0af05c55431625edb9a4a79836a73a498b11beec`
- Worker: `sha256:cda951a92149a1de30c0725ca84d7e227acf8b6e348129e23f8f0c5e0dc7d1d2`

Packaging changes:

- Added GitHub Actions CI for backend tests, backend builds, frontend build,
  Compose config, migration check, image build, SBOM generation, and Trivy scan
  artifacts.
- Added tag-triggered GHCR image publishing with immutable version and
  `sha-<commit>` tags for API, Web, and worker images.
- Added `mcxctl` to the API image and a production Compose `migrate` profile.
- Added `MULTICODEX_API_IMAGE` and `MULTICODEX_WEB_IMAGE` image variables for
  immutable deployment tags.
- Added release, migration, TLS/reverse-proxy, SBOM/scan, deploy, and rollback
  operator documentation.

The first API image build failed because local `.pnpm-store` cache files were
included in the Docker build context and one Windows-backed cache entry was not
readable. `.dockerignore` now excludes `.pnpm-store` and `pnpm-store`; the API
image build then passed.

## M5 Observability, DR, Audit, And Runbooks

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Fixed dev image refresh | `docker build -f deployments/docker/Dockerfile.dev -t multi-codex/dev:go1.25-node25.9-pnpm11.7 .` | passed |
| PostgreSQL client version | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev pg_dump --version` | passed, `pg_dump (PostgreSQL) 18.4` |
| M5 package tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/observability ./internal/api ./internal/mcp ./internal/retention ./cmd/mcxctl` | passed |
| Grafana JSON validation | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev jq empty deployments/observability/grafana-dashboard.json` | passed |
| Full backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| Backup/restore drill | disposable PostgreSQL database plus run/artifact/worktree roots through `mcxctl backup`, `mcxctl restore`, and `mcxctl audit-verify` | passed |

Backup/restore drill evidence:

```json
{
  "artifact_archive": true,
  "database_dump": true,
  "run_archive": true,
  "worktree_archive": true
}
```

```json
{
  "artifact_restore": true,
  "database_restore": true,
  "run_restore": true,
  "worktree_restore": true
}
```

```json
{
  "valid": true,
  "total": 0,
  "legacy": 0,
  "hashed": 0
}
```

Operations changes:

- Added operational Prometheus metrics for queue depth, worker terminal
  failures, audit ship health, retention cleanup health, and telemetry push
  failures.
- Added Prometheus alert rules under `deployments/observability/`.
- Added a Grafana dashboard skeleton under `deployments/observability/`.
- Added operator docs for observability, alerts, RPO/RTO, backup/restore drills,
  audit export/ship, and retention approval.
- Added incident runbooks for OIDC outage, database outage, stuck queue,
  runaway worker, failed audit ship, and compromised worker credentials.

The first database restore drill failed because the fixed dev image had
`pg_dump` 15 while the Compose database was PostgreSQL 18. The dev image now
installs `postgresql-client-18` from PGDG. Rebuilding the fixed dev image and
rerunning the drill resolved the mismatch.

## M6 Product Pilot Readiness

Additional verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Pilot Git Sync tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/config ./internal/api ./internal/mcp ./internal/gitsync ./internal/workflow` | passed |
| API pilot drill | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./internal/workflow ./internal/api` | passed |
| Pilot evidence verifier | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./cmd/mcxctl` | passed |

Pilot readiness changes:

- Added production fail-closed Git Sync live validation. API and MCP Gateway
  reject production live mode unless
  `MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true` and the selected credential provider
  is configured.
- Kept `MULTICODEX_GIT_SYNC_MODE=dry-run` as the default in local and
  production templates.
- Added API and MCP tests that exercise live GitHub PR creation through an
  `httptest` provider after the workflow gates have passed and `pr_publish` has
  been approved. The tests assert `dry_run=false`, `credential_resolved=true`,
  a provider PR URL, and `auto_merge=false`.
- Added an API-level pilot drill that provisions organization, project,
  repository, Skills, Agent Profiles, executor node, and task through public API
  endpoints, runs feature/scope/test/audit/approval/Git Sync gates, creates a
  dry-run publish result, then switches the same governed task to live provider
  PR creation against an `httptest` GitHub API.
- Added `mcxctl pilot-verify`, which checks database evidence for the governed
  pilot workflow plus strict external evidence files for audit ship, backup,
  restore, and sign-off. Non-strict mode is available for preflight checks; the
  strict mode is the operator sign-off gate.
- Fixed workflow completion semantics so `publish_prepared` remains dry-run
  evidence and does not block the later live `git_publish_pr` action. The
  workflow reaches `completed` only after a `published` Git Sync run.
- Added a product pilot runbook and evidence template covering OIDC groups,
  organization/project/repository provisioning, Skills, Agent Profiles,
  executor nodes, dry-run evidence review, live PR creation, rollback, audit
  export, restore exercise, token rotation, and sign-off.

### Live Pilot On `Chiiz0/Test`

After the operator supplied the private test repository
`https://github.com/Chiiz0/Test.git`, a real M6 pilot was executed on
2026-06-30 from the fixed dev image against an isolated Compose project named
`multi_codex_pilot`.

| Check | Command | Result |
| --- | --- | --- |
| GitHub repository access | `gh repo view Chiiz0/Test --json defaultBranchRef,isPrivate,viewerPermission,url` | passed, private repository, viewer permission `ADMIN`, default branch `main` |
| Pilot branch prepared | `git push -u origin codex/pilot-production-readiness-20260630-164319` | passed, commit `47294bd869a9fefea88e81926f9df667e2099ea1` |
| Pilot API/PostgreSQL startup | `docker compose -p multi_codex_pilot -f deployments/docker/compose.dev.yaml up -d postgres api-dev` with API port `18280` | passed, `/healthz` returned `200` |
| Governed dry-run workflow | API calls created organization/project/repository, Skills, Agent Profiles, executor node, task, feature/test/audit runs, scope check, `pr_prepare` approval, `git_prepare_pr`, `pr_publish` approval, and dry-run `git_publish_pr` | passed |
| Live PR creation | API restarted with `MULTICODEX_GIT_SYNC_MODE=live`, `MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true`, and `GITHUB_TOKEN` from the authenticated GitHub CLI keyring, then reran only `git_publish_pr` | passed |
| Provider PR state | `gh pr view 1 --repo Chiiz0/Test --json url,state,isDraft,mergeStateStatus,autoMergeRequest,headRefName,baseRefName` | passed, PR open, clean, not draft, `autoMergeRequest=null` |
| Audit verification | `docker compose -p multi_codex_pilot -f deployments/docker/compose.dev.yaml run --rm dev go run ./cmd/mcxctl audit-verify` | passed, `valid=true`, `hashed=34` |
| Audit seal and ship | `go run ./cmd/mcxctl audit-seal` plus `go run ./cmd/mcxctl audit-ship -target file:///workspace/.data/pilot-evidence/.../audit-ship-target` | passed, receipt `status=shipped` |
| Backup | `go run ./cmd/mcxctl backup -output /workspace/.data/backups/pilot-019f17b4-c146-76bb-85b4-dfcc2423eb6a` | passed, database/artifact/run/worktree archives present |
| Restore drill | `go run ./cmd/mcxctl restore` into disposable database `multi_codex_restore_fixed_20260630164319`, then `migrate` and `audit-verify` | passed, database/artifact/run/worktree restore true and restored audit hash valid |
| Strict pilot verifier | `go run ./cmd/mcxctl pilot-verify -task-id 019f17b4-c146-76bb-85b4-dfcc2423eb6a -audit-ship-receipt ... -backup-manifest ... -restore-evidence ... -signoff ...` | passed, `valid=true` |

Live pilot evidence:

- Task ID: `019f17b4-c146-76bb-85b4-dfcc2423eb6a`
- Task key: `PILOT-20260630-164319`
- Repository: `https://github.com/Chiiz0/Test.git`
- Target branch: `codex/pilot-production-readiness-20260630-164319`
- Changed file: `docs/multi-codex-pilot.md`
- Feature run: `019f17b4-c166-7909-945c-4c2a376972c3`
- Test run: `019f17b4-cb9d-73d1-a3ce-21e6b14a469d`
- Audit run: `019f17b4-d1b8-7e15-8fc1-b053c1809bee`
- Dry-run publish run: `019f17b4-d804-7cce-a2cd-b9b61e5cdbc4`
- Live publish run: `019f17b4-e308-7f88-b9bc-cc99b349ee51`
- Provider PR: `https://github.com/Chiiz0/Test/pull/1`
- Evidence root: `.data/pilot-evidence/019f17b4-c146-76bb-85b4-dfcc2423eb6a`

The live publish result recorded `dry_run=false`,
`credential_provider=env`, `credential_resolved=true`, and
`auto_merge=false`. The provider PR remains reviewable by humans and was not
auto-merged. The local pilot used local owner auth for API driving; production
OIDC group mapping remains covered by the M1/M2 automated tests and the pilot
runbook for the target deployment.

## Final Production-Readiness Verification

Final verification on 2026-06-30:

| Check | Command | Result |
| --- | --- | --- |
| Fixed dev image refresh | `docker build -f deployments/docker/Dockerfile.dev -t multi-codex/dev:go1.25-node25.9-pnpm11.7 .` | passed |
| Full backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| Backend build | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go build ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd ./cmd/mcxctl` | passed |
| Frontend build | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev bash -c "pnpm --dir apps/web install --frozen-lockfile && pnpm --dir apps/web build"` | passed |
| Migration smoke | `docker compose -f deployments/docker/compose.dev.yaml up -d --wait postgres` plus `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go run ./cmd/mcxctl migrate` | passed |
| Dev Compose config | `docker compose -f deployments/docker/compose.dev.yaml config` | passed |
| Production Compose config | `POSTGRES_PASSWORD=final-temp-postgres-password-20260630 docker compose -f deployments/docker/compose.yaml --profile migrate config` | passed |
| API image build | `docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:final-verify .` | passed |
| Web image build | `docker build -f deployments/docker/Dockerfile.web -t multi-codex/web:final-verify .` | passed |
| Worker image build | `docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:final-verify .` | passed |
| Compose smoke | `docker compose -f deployments/docker/compose.dev.yaml up -d --wait postgres api-dev mcp-gateway-dev worker-agentd-dev web-dev` with temporary host ports, then `curl.exe` health checks | passed |
| Post-pilot-drill backend tests | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go test ./...` | passed |
| Post-pilot-drill backend build | `docker compose -f deployments/docker/compose.dev.yaml run --rm dev go build ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd ./cmd/mcxctl` | passed |
| Post-pilot-drill API image build | `docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:final-verify .` | passed |

Final image IDs:

- Dev: `sha256:2c73ac410e5c73f9b8025a5a684c657f6fa2b99034dcb1cdd983f0cdbfacc16f`
- API: `sha256:24346349d66b49bf3b32cebe3cb0fdcef4c1c144fc50af94e8d4a80ffe79b4a4`
- Web: `sha256:ddd292fa1ae699788194b9f85a7e2e19bba108cae04a84dda8ffe57f332c1e0e`
- Worker: `sha256:e15be208ae283ea996a6a27402063b0ed3143126345131112d7d429b72054e30`

Compose smoke endpoints:

| Service | URL | Status |
| --- | --- | --- |
| API | `http://localhost:18180/healthz` | `200` |
| MCP Gateway | `http://localhost:18190/healthz` | `200` |
| worker-agentd | `http://localhost:17170/healthz` | `200` |
| Web Console | `http://localhost:13100` | `200` |

The final test suite includes coverage for production fail-closed config,
OIDC authentication and session flows, RBAC and resource isolation, audit hash
verification, audit seal/ship, backup/restore helpers, worker isolation,
structured secret redaction, command/dependency policies, MCP authorization,
and Git Sync dry-run/live PR creation with auto-merge disabled.
