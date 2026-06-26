# MVP Status

Last verified: 2026-06-26.

## Completed Slice

- Fixed Docker development image: `multi-codex/dev:go1.25-node25.9-pnpm11.7`.
- PostgreSQL 18 compose service with the correct `/var/lib/postgresql` volume mount.
- Idempotent `mcxctl migrate` migration runner.
- PostgreSQL-backed API and MCP Gateway storage.
- Seed organization, project, and repository for local development.
- Seed local user, membership, Skills, Agent Profiles, and executor nodes.
- Skill version history is queryable through `GET /api/v1/skills/{skill_id}/versions` and visible in the Web Console.
- PostgreSQL resource endpoints guard UUID-shaped path filters and return JSON arrays for invalid list filters instead of Postgres cast errors.
- OIDC-ready API and MCP auth mode with RS256/JWKS bearer-token validation.
- Persistent OIDC subject mapping to users and default-organization memberships.
- Configurable OIDC group-to-role and group-to-organization mapping with audited API/MCP mapping decisions.
- Web Console Session control for bearer-token API calls plus audited `POST /api/v1/auth/logout`.
- Server-side OIDC bearer-token revocation denylist stores token hashes, rejects revoked tokens in API/MCP auth paths, and cleans expired revocations through retention cleanup.
- OIDC browser session exchange stores only opaque session-token hashes server-side, authenticates API calls via HttpOnly cookies, revokes sessions on logout, and cleans expired/revoked sessions through retention cleanup.
- OIDC authorization-code login uses PKCE, nonce validation, one-time login state consumption, HttpOnly browser sessions, and audited login callback decisions.
- OIDC token endpoint client authentication supports public PKCE-only (`none`), `client_secret_post`, and `client_secret_basic`.
- OIDC back-channel logout verifies signed logout tokens, requires the back-channel event claim, and revokes browser sessions by upstream `sid` or subject fallback.
- OIDC login states are cleaned through retention cleanup and reported in the `auth_login_states` result section.
- Task Envelope validation.
- Resource-aware policy validation for repositories, Skills, Agent Profiles, and executor availability.
- Organization, task create, validate, start, workflow, run events, live run-event stream, run artifacts, run finish, scope check, approvals, tool calls, and audit log API.
- Artifact metadata persistence for rendered worker files and PR body output.
- Audited artifact content API with size limiting for worker logs, diffs, results, prompts, and memory-backed PR bodies.
- Tamper-evident audit log hash chaining for new audit rows.
- Optional append-only JSONL audit export through `MULTICODEX_AUDIT_EXPORT_PATH`.
- Audit seal/ship support through shared CLI/API bundle logic, including file, HTTP/S, S3 Object Lock, and opt-in scheduled API seal-and-ship worker with audited success/failure rows.
- Workflow gates for feature, scope, test, audit, approval, and git sync.
- Git Sync PR preparation includes a concrete `pr_publish_plan` with provider, branches, push command, required approval, credential requirement, and `auto_merge=false`.
- Git Sync PR publish supports a separate `pr_publish` approval gate, dry-run request preparation by default, and live GitHub/GitLab PR creation when explicitly configured with direct provider tokens or the shared env/file/Vault Git credential resolver.
- MCP Gateway Streamable HTTP-style `/mcp` endpoint:
  - `initialize`
  - `ping`
  - `tools/list`
  - `tools/call`
  - Accept negotiation for JSON/SSE
  - notification `202 Accepted` handling
  - SSE GET through metrics instrumentation
  - generated/client-provided `MCP-Session-Id` handling
  - configurable session TTL through `MULTICODEX_MCP_SESSION_TTL`
  - configurable active-stream fanout polling through `MULTICODEX_MCP_LIVE_FANOUT_INTERVAL`
  - `MCP-Session-Expires-At` headers and expired-session `404` handling
  - SSE event ids plus audited `Last-Event-ID` resume handling
  - persisted `mcp_sessions` and `mcp_session_events` for replayable ready/heartbeat events across gateway restarts
  - active SSE streams deliver persisted events appended by other gateway replicas through PostgreSQL LISTEN/NOTIFY plus polling fallback, without waiting for reconnect
  - audited `mcp.session_initialize`, `mcp.session_notification`, `mcp.session_stream_open`, `mcp.session_resume`, `mcp.session_notify_fanout`, `mcp.session_fanout`, and `mcp.session_expired`
- MCP Gateway tools:
  - `organization_list`
  - `organization_create`
  - `policy_validate_task`
  - `task_create`
  - `task_list`
  - `task_get`
  - `worker_spawn`
  - `worker_status`
  - `worker_logs`
  - `worker_result`
  - `queue_status`
  - `queue_dispatch`
  - `repo_scope_check`
  - `test_run_required`
  - `audit_run`
  - `approval_request`
  - `approval_status`
  - `git_prepare_pr`
  - `git_publish_pr`
- Local executor lifecycle:
  - render run directory
  - write `task.json`
  - write `prompt.md`
  - write `AGENTS.override.md`
  - write `worker.log`
  - write `result.json`
  - write `diff.patch`
  - append run events
  - record artifacts
- Docker executor mode:
  - bootstraps the missing seeded local `demo-service.git` remote into a real bare Git repository for fresh-checkout verification
  - prepares repo mirror/workspace when a repository is available
  - mounts `/runs` and `/workspace`
  - invokes `codex exec` from the fixed Codex worker image
  - runs Docker network `none` by default and `bridge` only when task/profile policy allows it
  - injects requested worker secret env names only when task network, Agent Profile config, deployment allowlist, and configured secret provider are all present
  - resolves worker secrets through default `env`, reloadable JSON `file`, or Vault KV v2 provider
  - records local, Docker, SSH, timeout, error, and worker secret decisions in audit logs
- Git Sync provider credentials:
  - resolves `GITHUB_TOKEN` and `GITLAB_TOKEN` through the same secret resolver family used by worker secrets
  - supports default process env, reloadable JSON secret files, and Vault KV v2
  - records credential provider and resolved status in run events and API/MCP audit logs without storing secret values
- Worker timeout enforcement:
  - uses Agent Profile `timeout_seconds` before `MULTICODEX_WORKER_DEFAULT_TIMEOUT`
  - marks expired runs as `timed_out`
  - releases scheduler capacity on timeout
  - attempts Docker container cleanup on timeout
- worker-agentd remote execution service:
  - health check
  - controlled run intake
  - optional Bearer-token protection for HTTP run intake and remote result/log retrieval
  - forced-command stdin/stdout mode
  - remote log retrieval
  - result retrieval
- SSH executor:
  - host key fingerprint verification
  - verified-node dispatch through agentd
  - forced-command SSH stdin/stdout transport for nodes without `agentd_url`
  - remote log/result artifact collection
- Basic worker-pool scheduling:
  - assigns active executor nodes by executor kind
  - enforces per-node `capacity.concurrency` before starting active runs
  - persists `executor_node_id` on runs
  - records assigned node in `worker_spawn` events
  - enqueues capacity-full worker starts as durable `queued` runs instead of overcommitting
  - orders queued runs by `queue_priority` and creation time
  - dispatches queued runs through an API background worker and API/MCP manual controls
  - retries failed workers according to Agent Profile `retry_max_attempts` or `retry.max_attempts`
  - includes structured backpressure details and HTTP `Retry-After` on capacity pressure
  - selects nodes by utilization, available slots, heartbeat freshness, and node age
  - exposes per-node available slots, utilization, selection rank, and selection reason in API/MCP queue snapshots
- API/MCP observability:
  - `/metrics`
  - Prometheus text metrics via `?format=prometheus`
  - OpenTelemetry-compatible JSON metrics via `?format=otlp`
  - normalized dynamic route labels
  - run count, active-run, and run-duration metrics by role/executor/status
  - HTTP request and completed run duration histograms in Prometheus and OTLP output
  - optional OTLP JSON push from API and MCP Gateway
  - trace id response header and W3C `traceparent` propagation
	- Enterprise operations:
	  - `mcxctl retention-cleanup`
	  - opt-in API retention cleanup worker with audited dry-run/delete decisions
	  - expired MCP replay session/event cleanup through the same retention command and worker
	  - `mcxctl backup`
  - `mcxctl restore`
  - `mcxctl audit-verify`
  - `mcxctl audit-seal`
  - `mcxctl audit-ship`
- Web Console:
  - dashboard
  - create projects and repositories
  - create Task Envelopes
  - start worker runs
  - inspect run detail from the Runs page
  - stream run events live through `GET /api/v1/runs/{run_id}/events/stream` with polling fallback
  - run scope checks
  - inspect workflow gates
  - inspect run events and artifacts
  - read worker log, result, PR body, and diff artifact content
  - approval center
  - executor node management
  - organization provisioning and management
  - Skill and Agent Profile management
  - Skill version/history inspection
  - Queue view with queued runs, backpressure snapshots, and manual dispatch
  - audit log hash and MCP tool call query

## Verified Commands

```bash
make dev-image
make migrate-dev
make backend-test
make backend-build
go test ./internal/auth ./internal/api ./internal/store ./internal/config
go test ./internal/gitsync ./internal/config ./internal/api ./internal/mcp
go test ./internal/mcp -run 'TestStreamableHTTPStreamFansOut' -count=1
go test ./internal/api -run TestRunEventStreamSendsNewEventsAndAudits -count=1
go test ./internal/api -run TestSkillVersionHistoryAPI -count=1
go test ./cmd/worker-agentd ./internal/executor ./internal/config -run 'TestAgentD|TestRunSSHAgentD|TestFromEnvParsesRetentionConfig' -count=1
go test ./internal/executor -run TestPrepareWorkspaceBootstrapsMissingSeedDemoRepository -count=1 -v
MULTICODEX_TEST_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go test ./internal/store -run TestPostgresMCPSessionEventNotification -count=1 -v
pnpm --dir apps/web build
make frontend-build
make compose-config
docker compose -f deployments/docker/compose.dev.yaml up -d postgres api-dev mcp-gateway-dev web-dev
docker compose -f deployments/docker/compose.dev.yaml up -d worker-agentd-dev
go run ./cmd/mcxctl retention-cleanup -dry-run=true -max-age=1h
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go run ./cmd/mcxctl audit-verify -allow-legacy-hash-mismatch=true
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go run ./cmd/mcxctl audit-seal -allow-legacy-hash-mismatch=true -output .data/audit-seals/verify-20260626T065000Z
go run ./cmd/mcxctl audit-ship -input .data/audit-seals/verify-20260626T065000Z -target file://.data/audit-ship
docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:verify .
docker build -f deployments/docker/Dockerfile.web -t multi-codex/web:verify .
docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:verify .
docker run --rm --entrypoint codex multi-codex/codex-worker:verify --version
```

Docker services verified healthy:

```text
postgres
api-dev
mcp-gateway-dev
web-dev
worker-agentd-dev
```

HTTP checks verified:

```text
GET  /healthz on API
GET  /healthz on MCP Gateway
GET  /api/v1/organizations
POST /api/v1/organizations
POST /api/v1/projects/{project_id}/tasks
POST /api/v1/tasks/{task_id}/start
GET  /api/v1/runs/{run_id}/events
GET  /api/v1/runs/{run_id}/artifacts
GET  /api/v1/artifacts/{artifact_id}/content
GET  /api/v1/audit-logs
POST /api/v1/executor-nodes/{node_id}/verify-host-key
GET  /metrics on API and MCP Gateway
GET  /metrics?format=otlp on API and MCP Gateway
POST /mcp with initialize, tools/list, tools/call
POST /mcp tools/call with organization_create, organization_list
POST /tools/call with task_create, worker_spawn, worker_status
GET  /api/v1/queue
POST /api/v1/queue/dispatch
POST /mcp tools/call with queue_status and queue_dispatch
```

Browser verification confirmed:

- tasks render in the console
- `New Task` creates a draft task
- `Start` triggers a run
- run events render during execution through the API EventSource stream, with REST polling fallback
- Runs page exposes selectable Run Detail with live events, artifact content, and result JSON
- Orgs page renders provisioned organizations and the `Provision` action
- Skills page creates network-enabled Agent Profiles with requested secret env refs
- workflow gates refresh through `repo_scope_check`, `test_run_required`, `audit_run`, approval, `git_prepare_pr`, and `git_publish_pr`
- Queue page shows queued worker runs, docker/ssh backpressure snapshots, and manual dispatch status
- Session control build exposes standard Sign in, bearer-token Connect, and Sign out controls with constrained three-column button layout

## Current Executor Modes

`MULTICODEX_EXECUTOR_MODE=mock` is the default and verified local development path. It renders the same run assets and artifact records as Docker mode, but does not claim to perform AI code changes.

`MULTICODEX_EXECUTOR_MODE=docker` prepares workspace mounts and invokes `codex exec` from the fixed worker image. The image pins `@openai/codex@0.142.2` and verifies the `codex` binary at image build time. Worker containers use Docker network `none` by default; a task receives `bridge` networking only when its Task Envelope requests network and the selected Agent Profile permits it.

## Explicit Delivery Boundary

- Branch push and PR creation remain gated by explicit `pr_publish` approval. Git Sync can dry-run publish requests by default or create GitHub/GitLab PRs in live mode when credentials and remote branches are available; it never merges or auto-merges.
