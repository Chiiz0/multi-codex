# Verification Report

Last verified: 2026-06-26.

## Environment

- Fixed dev image: `multi-codex/dev:go1.25-node25.9-pnpm11.7`
- PostgreSQL: `postgres:18`
- API: `localhost:8080`
- MCP Gateway: `localhost:8090`
- Web Console: `localhost:3000`
- worker-agentd: `localhost:7070`
- Codex worker image: `multi-codex/codex-worker:go1.25-node-vite8` with `@openai/codex@0.142.2`

## Commands Run

```bash
scripts/migrate-dev.sh
go test ./...
go build ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd ./cmd/mcxctl
pnpm --dir apps/web build
docker compose -f deployments/docker/compose.dev.yaml config
docker compose -f deployments/docker/compose.yaml config
docker compose -f deployments/docker/compose.dev.yaml restart api-dev mcp-gateway-dev
docker compose -f deployments/docker/compose.dev.yaml up -d postgres api-dev mcp-gateway-dev web-dev
docker compose -f deployments/docker/compose.dev.yaml up -d worker-agentd-dev
curl http://localhost:8080/api/v1/queue
curl http://localhost:8090/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"queue_status","arguments":{}}}'
curl http://localhost:8090/mcp -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' -d '{"jsonrpc":"2.0","id":"session-init","method":"initialize"}'
curl --max-time 1 http://localhost:8090/mcp -H 'Accept: text/event-stream' -H 'MCP-Session-Id: <session>' -H 'Last-Event-ID: 7'
curl 'http://localhost:8080/metrics?format=prometheus' | rg 'multi_codex_http_request_duration_seconds_bucket|multi_codex_run_duration_seconds_bucket'
curl 'http://localhost:8080/metrics?format=otlp' | rg 'multi_codex_http_request_duration_seconds|multi_codex_run_duration_seconds'
go test ./cmd/worker-agentd ./internal/executor ./internal/config -run 'TestAgentD|TestRunSSHAgentD|TestFromEnvParsesRetentionConfig' -count=1
go test ./internal/gitsync ./internal/config ./internal/api ./internal/mcp
go test ./internal/api ./internal/mcp ./internal/observability ./internal/config
go test ./internal/store ./cmd/mcxctl
go test ./internal/mcp -run 'TestStreamableHTTPStreamFansOut' -count=1
go test ./internal/api -run TestRunEventStreamSendsNewEventsAndAudits -count=1
go test ./internal/api -run TestSkillVersionHistoryAPI -count=1
MULTICODEX_TEST_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go test ./internal/store -run TestPostgresMCPSessionEventNotification -count=1 -v
go run ./cmd/mcxctl retention-cleanup -dry-run=true -max-age=1h
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go run ./cmd/mcxctl audit-verify -allow-legacy-hash-mismatch=true
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' go run ./cmd/mcxctl audit-seal -allow-legacy-hash-mismatch=true -output .data/audit-seals/verify-20260626T065000Z
go run ./cmd/mcxctl audit-ship -input .data/audit-seals/verify-20260626T065000Z -target file://.data/audit-ship
go test ./internal/config ./internal/store ./internal/api ./cmd/mcxctl
MULTICODEX_API_LISTEN=:18082 MULTICODEX_DATABASE_URL='' MULTICODEX_QUEUE_ENABLED=false MULTICODEX_AUDIT_SHIP_ENABLED=true MULTICODEX_AUDIT_SHIP_INTERVAL=1s MULTICODEX_AUDIT_SEAL_ROOT=.data/audit-ship-scheduler-live/seals MULTICODEX_AUDIT_SHIP_TARGET=file://.data/audit-ship-scheduler-live/target go run ./cmd/api
docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:verify .
docker build -f deployments/docker/Dockerfile.web -t multi-codex/web:verify .
docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:verify .
docker run --rm --entrypoint codex multi-codex/codex-worker:verify --version
MULTICODEX_API_LISTEN=:18080 MULTICODEX_DATABASE_URL='' MULTICODEX_AUTH_MODE=oidc MULTICODEX_OIDC_ISSUER=https://issuer.example MULTICODEX_OIDC_AUDIENCE=multi-codex MULTICODEX_OIDC_JWKS_URL=http://127.0.0.1:9/jwks go run ./cmd/api
MULTICODEX_MCP_LISTEN=:18090 MULTICODEX_DATABASE_URL='' MULTICODEX_AUTH_MODE=oidc MULTICODEX_OIDC_ISSUER=https://issuer.example MULTICODEX_OIDC_AUDIENCE=multi-codex MULTICODEX_OIDC_JWKS_URL=http://127.0.0.1:9/jwks go run ./cmd/mcp-gateway
MULTICODEX_API_LISTEN=:18081 MULTICODEX_DATABASE_URL='' MULTICODEX_RETENTION_ENABLED=true MULTICODEX_RETENTION_INTERVAL=1s MULTICODEX_RETENTION_MAX_AGE=1h MULTICODEX_RETENTION_DRY_RUN=true go run ./cmd/api
```

Worker image verification returned:

```text
codex-cli 0.142.2
```

## HTTP Checks

Verified:

- `GET http://localhost:8080/healthz`
- `GET http://localhost:8090/healthz`
- `GET http://localhost:3000`
- `GET http://localhost:8080/api/v1/auth/me`
- `GET http://localhost:8080/api/v1/organizations`
- `POST http://localhost:8080/api/v1/organizations`
- `GET http://localhost:18080/api/v1/auth/me` without Authorization returns `401`
- `POST http://localhost:18090/mcp` without Authorization returns `401`
- `GET http://localhost:8080/api/v1/skills`
- `GET http://localhost:8080/api/v1/runs/{run_id}/artifacts`
- `GET http://localhost:8080/api/v1/artifacts/{artifact_id}/content`
- `GET http://localhost:8080/api/v1/audit-logs`
- `GET http://localhost:8080/api/v1/queue`
- `POST http://localhost:8080/api/v1/queue/dispatch`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `initialize`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `tools/list`
- `GET http://localhost:8090/mcp` with `Accept: text/event-stream`, `MCP-Session-Id`, and `Last-Event-ID`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `tools/call` for `task_list`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `tools/call` for `queue_status`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `tools/call` for `organization_create`
- `POST http://localhost:8090/mcp` with `Accept: application/json, text/event-stream` and `tools/call` for `organization_list`
- `GET http://localhost:7070/healthz`
- `POST http://localhost:7070/v1/runs`
- `GET http://localhost:7070/v1/runs/{run_id}/result`
- `GET http://localhost:7070/v1/runs/{run_id}/logs`
- `POST http://localhost:8080/api/v1/executor-nodes/{node_id}/verify-host-key`
- `GET http://localhost:8080/metrics`
- `GET http://localhost:8090/metrics`
- `GET http://localhost:8080/metrics?format=prometheus`
- `GET http://localhost:8090/metrics?format=prometheus`
- `GET http://localhost:8080/metrics?format=otlp`
- `GET http://localhost:8090/metrics?format=otlp`

## Web Console Coverage

The production web build verifies the typed client and routes for:

- Dashboard
- Task Detail
- Run Detail
- Live run-event stream with polling fallback
- Log and artifact content viewer
- Diff/result viewer through the shared run artifact inspector
- Approval Center
- Node Management
- Queue view with queued runs, executor backpressure, and manual dispatch
- Organization Management
- Skill/Profile Management
- Skill version-history query
- Agent Profile network and requested secret-env refs
- Audit Log query
- MCP Tool Call query

## Workflow Verification

Unit coverage:

- `internal/workflow` full gate path test.
- `internal/workflow` blocker tests for scope violation, test failure, audit blocker, and approval rejection.

Verified API flow:

1. Create Task Envelope.
2. Start feature worker.
3. Observe run events:
   - `worker_spawn`
   - `workspace_seed_repo_bootstrap` when the seed demo repo is first created
   - `executor_prepare`
   - `worker_log`
   - `worker_result`
4. Observe artifacts:
   - `task_envelope`
   - `prompt`
   - `agent_override`
   - `worker_log`
   - `result`
   - `diff`
5. Run scope check.
6. Run test worker.
7. Run audit worker.
8. Request and approve PR preparation.
9. Run Git Sync PR body preparation.
10. Request and approve PR publish preparation.
11. Run Git Sync PR publish dry-run.

Git Sync output now includes a publish plan that remains gated and does not auto-merge:

```json
{
  "required_approval": "pr_publish",
  "provider_operation": "create_pull_request",
  "credential_required": "provider token",
  "auto_merge": false
}
```

Dry-run PR publish verification:

```json
{
  "run": {
    "id": "019f022d-29c5-79cd-bbc6-b089f1a597d5",
    "status": "succeeded",
    "role": "git_sync"
  },
  "result": {
    "status": "publish_prepared",
    "dry_run": true,
    "auto_merge": false,
    "provider": "local",
    "required_approval": "pr_publish",
    "credential_required": "provider token"
  }
}
```

Scheduler assignment was verified with a fresh feature run:

```json
{
  "run_id": "019f0221-6ec9-7578-9a3b-37e7ea83df59",
  "executor": "docker",
  "executor_node_id": "00000000-0000-7000-8000-000000000301"
}
```

The initial `worker_spawn` event included the same `executor_node_id`.

Scheduler capacity was verified in `internal/store` unit coverage. The memory scheduler starts the first run on a single-slot node, returns `ErrNoCapacity` for a second active run, then allows the second run after the first reaches a terminal status.

Structured backpressure was verified in `internal/scheduler` unit coverage. Capacity conflicts include retry-after seconds, available slots, and per-node active run/concurrency state.

Multi-node fairness is covered by `internal/store` and `internal/scheduler` unit tests. The scheduler selects eligible nodes by utilization ratio, then available slots, then heartbeat freshness and node age. Backpressure snapshots expose `available_slots`, `utilization`, `selection_rank`, and `selection_reason` for each node.

Persisted queue dispatch was verified in `internal/api` and `internal/mcp` unit coverage. A queued run is listed, dispatched through the real store capacity check, starts the executor, and emits API/MCP tool and audit records.

Scheduler capacity and persisted queue dispatch were also verified against the running PostgreSQL dev stack. The seeded Docker executor node was temporarily marked non-active, then an API-created feature task was started:

```json
{
  "start_status": 202,
  "queued": true,
  "retry_after": 10,
  "run_id": "019f0273-902a-7aba-b1af-550dcaac3e7a",
  "queued_reason": "capacity_full",
  "available_slots": 0
}
```

After the node was restored to active, the API queue worker dispatched the same run on its scheduled interval:

```json
{
  "run_id": "019f0273-902a-7aba-b1af-550dcaac3e7a",
  "status": "succeeded",
  "executor": "docker",
  "executor_node_id": "00000000-0000-7000-8000-000000000301",
  "events": ["worker_queued", "worker_spawn", "executor_prepare", "worker_log", "worker_result"],
  "audit_actions": ["api.worker_enqueue", "api.queue_dispatch", "worker.local_lifecycle"]
}
```

The MCP Gateway exposes the same queue state through `queue_status`; `tools/list` includes both `queue_status` and `queue_dispatch`.

API run-event streaming coverage opens a real HTTP SSE response, receives existing events with SSE `id:` fields, appends a new run event through the store, verifies the active stream receives that event without reconnecting, and asserts `api.run_event_stream_open` plus `api.run_event_stream_close` audit rows.

Live dev-stack run-event stream smoke created run `019f0312-fa36-7da2-a8ca-22ed776025cd`, opened `/api/v1/runs/{run_id}/events/stream`, posted a new `stream_smoke` event through the run-events API, and observed the same SSE connection receive:

```text
id: 6
event: message
data: {"event_type":"stream_smoke","message":"stream smoke event",...}
```

Live dev-stack Skill version smoke registered a unique Skill twice and queried `/api/v1/skills/{skill_id}/versions`; the response returned `v2/live-hash-v2` and `v1/live-hash-v1` in version history.

MCP Streamable HTTP conformance checks cover Accept negotiation, SSE GET through metrics instrumentation, notification handling, Origin guard, initialize response shape, generated `MCP-Session-Id` values, and `mcp.session_*` lifecycle audit rows. Live dev-stack verification returned:

```json
{
  "empty_accept_status": 406,
  "queue_status_with_accept": 200,
  "queued": 0,
  "initialize_session": "mcp_session_91d29c479cf0e7c6",
  "session_audit": "mcp.session_initialize"
}
```

The running PostgreSQL dev stack was also verified with two active Docker nodes: the seeded one-slot `local-docker` node and an API-registered `large-docker-live` node with `capacity.concurrency=4`. The first live feature run selected the larger idle node, while the immediately following run selected the idle one-slot node after the larger node reached 1/4 utilization:

```text
BALANCE-LIVE-1782453992-1 -> large-docker-live
BALANCE-LIVE-1782453992-2 -> local-docker
```

The live API and MCP queue snapshots both returned node analytics fields:

```json
{
  "name": "large-docker-live",
  "active_runs": 0,
  "concurrency": 4,
  "available_slots": 4,
  "utilization": 0,
  "selection_rank": 1,
  "selection_reason": "eligible_capacity"
}
```

Final workflow state:

```json
{
  "next_actions": ["completed"],
  "blocked_reasons": [],
  "ready_for_pr": true
}
```

## SSH Executor Verification

Verified:

1. SSH executor node host key fingerprint was verified.
2. SSH Agent Profile was created.
3. SSH Task Envelope was created.
4. Worker run was dispatched through `worker-agentd`.
5. Run events included:
   - `worker_spawn`
   - `workspace_seed_repo_bootstrap` when the seed demo repo is first created
   - `executor_prepare`
   - `ssh_agentd_run`
   - `worker_result`
6. Artifacts included:
   - `task_envelope`
   - `prompt`
   - `agent_override`
   - `worker_log`
   - `result`
   - `remote_result`
   - `diff`

Observed run result:

```json
{
  "status": "succeeded",
  "executor": "ssh",
  "result": "succeeded",
  "summary": "worker-agentd accepted and recorded a controlled remote run payload"
}
```

Forced-command mode was also verified with:

```bash
printf '{"run_id":"forced_verify","task_id":"task_forced","role":"feature","prompt":"forced command verify"}\n' \
  | MULTICODEX_RUN_ROOT="$(mktemp -d)" go run ./cmd/worker-agentd --forced-command
```

HTTP agentd Bearer-token smoke verification started a temporary listener on `:18071` and returned:

```text
unauth=401 auth=201 logs=200
```

The forced-command result included `worker_log_content`, allowing SSH stdout transport to collect logs without a second remote shell command.

The API SSH executor forced-command transport is covered by unit verification with a fake `ssh` binary. The test proves the executor sends the controlled run JSON over stdin, parses result JSON from stdout, captures SSH stderr into `worker.log`, writes `remote-result.json`, finalizes the run, and emits `worker.ssh_forced_command_run` audit logs.

## Operations Verification

Metrics endpoints were verified in JSON, Prometheus text, and OpenTelemetry-compatible JSON modes. Prometheus output includes normalized route labels such as:

```text
multi_codex_http_requests_total{service="multi-codex-api",method="GET",path="/api/v1/projects/{id}/tasks",status="200"} 2
```

Run metrics are computed from stored runs on each metrics request. Prometheus output includes series such as:

```text
multi_codex_runs_total{service="multi-codex-api",role="feature",executor="docker",status="succeeded"} 1
multi_codex_active_runs{service="multi-codex-api",role="feature",executor="docker",status="running"} 1
multi_codex_run_duration_seconds_total{service="multi-codex-api",role="feature",executor="docker",status="succeeded"} 0.150000
multi_codex_run_duration_seconds_bucket{service="multi-codex-api",role="feature",executor="docker",status="succeeded",le="1"} 1
multi_codex_http_request_duration_seconds_bucket{service="multi-codex-api",method="GET",path="/api/v1/runs/{id}/events",le="0.05"} 1
```

OTLP JSON output includes a `resourceMetrics` envelope and metric names such as `multi_codex_http_requests_total`, `multi_codex_http_request_duration_seconds`, `multi_codex_active_runs`, and `multi_codex_run_duration_seconds`. API and MCP responses also propagate W3C `traceparent` headers when a valid traceparent is provided.

Optional OTLP push is covered by API and MCP unit tests with `httptest.Server`; the tests verify that configured push sends a `resourceMetrics` payload containing duration histogram metrics. Push remains disabled unless `MULTICODEX_TELEMETRY_PUSH_URL` is set.

OIDC API mode was verified with a temporary in-memory API on `:18080`. A request without a Bearer token returned:

```json
{
  "error": "authentication required"
}
```

Unit coverage verifies RS256/JWKS token validation and audience mismatch rejection in `internal/auth`.
Unit coverage verifies stable in-memory external-user upsert behavior in `internal/store`.
Unit coverage verifies OIDC group-to-role and group-to-organization claim mapping, plus env parsing for ordered mapping policy.
Unit coverage verifies OIDC logout revokes the bearer-token hash, later API requests with the same token return `401`, MCP requests with a revoked token return `401`, and both paths emit auth-denied audit rows.
Unit coverage verifies `POST /api/v1/auth/session` exchanges a valid bearer token for an HttpOnly cookie, cookie-only `/auth/me` succeeds, logout revokes the session, the old cookie then returns `401`, and session create/logout audit rows are emitted.
Unit coverage verifies the OIDC authorization-code flow starts with PKCE and nonce, consumes a one-time login state, exchanges the code for an ID token, creates an HttpOnly browser session, redirects to the sanitized return path, and emits login/session audit rows. The same test verifies back-channel logout accepts a signed logout token with the required event claim, revokes the session by upstream `sid`, and the old cookie then returns `401`.
Live local-mode smoke verified `GET /api/v1/auth/login?return_to=%2F%23audit` returns `302 Location: /#audit`, so the new OIDC login entrypoint does not disrupt default local development auth.
Unit coverage verifies organization API provisioning, duplicate slug conflict behavior, and MCP organization list/create tools.
Unit coverage verifies worker timeout enforcement, `timed_out` run finalization, and scheduler capacity release after timeout.
PostgreSQL migration verification confirmed `users.external_provider` and `users.external_subject` columns exist.
Unit coverage verifies `MULTICODEX_AUDIT_EXPORT_PATH` writes append-only JSONL audit rows with entry hashes.

MCP OIDC mode was verified with a temporary in-memory gateway on `:18090`. A `tools/list` JSON-RPC request without a Bearer token returned:

```json
{
  "error": "authentication required"
}
```

Organization provisioning verification against the running PostgreSQL dev stack:

```json
{
  "api_create_status": 201,
  "api_list_found": true,
  "mcp_create_status": 200,
  "mcp_list_status": 200,
  "api_audit_entries": 1,
  "mcp_org_tool_calls": 2
}
```

Browser verification for the Web Console confirmed `#organizations` renders the Organization Management view, marks the `Orgs` nav item active, shows the seeded default organization plus API/MCP-created organizations, and exposes the `Provision` action.

Web Console session control verification against the running Docker dev stack:

```bash
curl -sf -X POST http://localhost:8080/api/v1/auth/logout
```

```json
{
  "mode": "local",
  "status": "logged_out"
}
```

The latest audit row for that request was `api.auth_logout` with actor `00000000-0000-7000-8000-000000000011`, resource type `auth_session`, and entry hash `e91c1789cd04f8200d213c573b6c25503d431d3dbd0e2085ac73c96726281fe2`.

Frontend build verification confirmed the sidebar Session control includes standard `Sign in`, password-style `Bearer token` input, `Connect`, and `Sign out` controls. The built CSS uses a three-column `auth-actions` grid with constrained button widths. The previous desktop/mobile browser screenshots remain under `.data/visual-verification/`; the in-app browser navigation surface did not complete during this pass, so this slice used TypeScript build, production bundle inspection, and HTTP smoke checks for the UI change.

PostgreSQL ID-shape guard verification after restarting the dev API:

```bash
curl -sf http://localhost:8080/api/v1/projects/proj_demo/agent-profiles
curl -sf http://localhost:8080/api/v1/projects/proj_demo/repositories
curl -i -s -X POST http://localhost:8080/api/v1/projects/proj_demo/agent-profiles \
  -H 'Content-Type: application/json' \
  -d '{"name":"bad","role":"feature","model":"gpt-5","executor":"docker"}'
```

The list endpoints returned `[]`, the create endpoint returned `400 Bad Request` with `{"error":"project_id must be a UUID"}`, and fresh API logs contained no `invalid input syntax` or `list agent profiles failed` errors.

Request-bound audit actor verification:

```json
{
  "action": "api.artifact_read",
  "actor_id": "00000000-0000-7000-8000-000000000011",
  "prev_hash": "6ac4a7902e2284897445ed3e31ffd9924730ac6b14ecaec5ec70ce9060d07e3e",
  "entry_hash": "a0fbb915f94f5aeab724e36ccc5bc1a4ca2e50c6631e6beb25273053bfea8402"
}
```

Retention dry-run produced a JSON summary with no errors:

```json
{
  "dry_run": true,
  "errors": [],
  "mcp_sessions": {
    "dry_run": true,
    "scanned_sessions": 1,
    "deleted_sessions": 1,
    "deleted_events": 1
  },
  "auth_token_revocations": {
    "dry_run": true
  }
}
```

MCP replay retention delete verification seeded `session.retention.live`, ran `mcxctl retention-cleanup -dry-run=false -max-age=1h`, and confirmed both `mcp_sessions` and `mcp_session_events` returned count `0` for that session afterward. The delete smoke also exercised the configured dev filesystem roots; future DB-only retention checks should point run/artifact/worktree roots at temporary directories.

Auth token revocation retention live verification inserted `live-expired-token-hash`, ran `mcxctl retention-cleanup -dry-run=false -max-age=1h` with run/artifact/worktree roots pointed at `.data/retention-db-only/*`, and received:

```json
{
  "auth_token_revocations": {
    "dry_run": false,
    "scanned": 1,
    "deleted": 1
  }
}
```

The follow-up PostgreSQL query returned `remaining=0` for that hash.

PostgreSQL migration verification confirmed `auth_sessions` exists. Unit coverage verifies expired browser sessions appear in retention dry-run/delete results, API retention audit payloads include `auth_sessions`, and expired or consumed OIDC login states are reported in `auth_login_states`.

Retention CLI verification with temporary filesystem roots returned an `auth_login_states` result section with dry-run counts:

```json
"auth_login_states": {
  "dry_run": true,
  "scanned": 0,
  "deleted": 0
}
```

Retention worker dry-run produced an audited system decision:

```json
{
  "action": "api.retention_cleanup",
  "actor_type": "system",
  "actor_id": "api",
  "scanned": 1,
  "deleted": 1,
  "dry_run": true,
  "entry_hash": "3bc69c35cb86614b72ece6c74bf4d1e4dcb3d3fb01b1d6614b63ac02f92348b6"
}
```

Backup command was verified with database dumping disabled and filesystem manifest generation enabled:

```bash
MULTICODEX_DATABASE_URL='' go run ./cmd/mcxctl backup -output "$BACKUP_DIR"
```

## Artifact and Audit Verification

Verified an SSH run artifact content read:

```json
{
  "kind": "worker_log",
  "content_type": "text/plain",
  "truncated": false,
  "limit_bytes": 2097152
}
```

Verified the next artifact read chained to the previous audit row:

```json
[
  {
    "action": "api.artifact_read",
    "prev_hash": "bdf04ab2b519efd8b71e5b5af313fbeca1e01eba09fb1b3e2e328f2a3d9a7508",
    "entry_hash": "6ac4a7902e2284897445ed3e31ffd9924730ac6b14ecaec5ec70ce9060d07e3e"
  },
  {
    "action": "api.artifact_read",
    "entry_hash": "bdf04ab2b519efd8b71e5b5af313fbeca1e01eba09fb1b3e2e328f2a3d9a7508"
  }
]
```

Verified a live mock worker run emitted a worker audit row:

```json
{
  "action": "worker.local_lifecycle",
  "actor_type": "worker",
  "resource_type": "run",
  "payload": {
    "executor_mode": "mock",
    "status": "succeeded"
  }
}
```

Unit coverage verifies worker secret-env decisions:

- profile `config.worker_secret_env` names are required
- `MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST` must contain the name
- task network must be enabled
- the configured `env` or `file` provider must resolve the name
- invalid, missing, or non-allowlisted names are skipped with audited reasons
- configured secret values are redacted from Docker worker output files and Docker output events
- file provider lookups reload the JSON secret file, so rotated local/Kubernetes secret mounts apply to the next worker start
- Vault KV v2 provider lookups use `/v1/<mount>/data/<path>`, token or token-file auth, optional namespace headers, and resolve requested names from `data.data`

Unit coverage verifies Git Sync provider credential handling:

- GitHub live PR creation can resolve `GITHUB_TOKEN` from a reloadable JSON secret file
- GitLab live merge-request creation can resolve `GITLAB_TOKEN` from Vault KV v2
- provider error responses redact the resolved credential before returning or persisting errors
- API and MCP publish paths include `credential_provider` and `credential_resolved` metadata in run events and audit rows
- API and MCP dry-run publish handler/tool tests assert those credential metadata fields and `auto_merge=false`

Unit and live coverage verify MCP session lifecycle:

- configurable `MULTICODEX_MCP_SESSION_TTL`
- configurable `MULTICODEX_MCP_LIVE_FANOUT_INTERVAL`
- `MCP-Session-Expires-At` response headers
- expired sessions return `404` and emit `mcp.session_expired`
- persisted `mcp_sessions` and `mcp_session_events`
- SSE streams include event `id:` fields backed by persisted per-session sequence numbers
- `Last-Event-ID` reconnects replay persisted SSE events before appending a new ready event
- active SSE streams use PostgreSQL LISTEN/NOTIFY plus persisted event reads, with interval polling fallback, to deliver events appended by another gateway replica without waiting for reconnect
- `mcp.session_resume`, `mcp.session_stream_open`, `mcp.session_notify_fanout`, and `mcp.session_fanout` audit rows include event cursor metadata

Unit coverage opens a real HTTP SSE response, appends a session event directly through the store to simulate another replica, and verifies the active stream receives `id: 2` with `source:"replica-b"` plus an audited `mcp.session_fanout` row. A separate notification test disables the polling path with a one-hour interval, publishes a synthetic session notification, verifies the stream receives `source:"notify"`, and asserts an audited `mcp.session_notify_fanout` row with `fanout_transport=postgres_listen_notify`.

The PostgreSQL integration test `TestPostgresMCPSessionEventNotification` was run against the dev database with `MULTICODEX_TEST_DATABASE_URL`; it creates a unique MCP session, subscribes with `LISTEN`, appends an event through `AppendMCPSessionEvent`, receives the committed `pg_notify` payload, and deletes the test session.

Live replay verification restarted `mcp-gateway-dev` between stream opens:

```text
--- first stream ---
id: 1
event: message
data: {"replayed_events":0,"resumed":false,"server":"multi-codex-mcp-gateway","type":"ready"}

--- replay stream ---
id: 1
event: message
data: {"replayed_events":0,"resumed":false,"server":"multi-codex-mcp-gateway","type":"ready"}

id: 2
event: message
data: {"replayed_events":1,"resumed":true,"server":"multi-codex-mcp-gateway","type":"ready"}
```

The PostgreSQL session row for `session.persist2` had `status=active` and `last_event_id=2`. The persisted event table contained two `ready` events with seq `1` and `2`. The latest `mcp.session_resume` audit row recorded `client_last_event_id:"0"`, `last_event_id:"2"`, and `replayed_events:1`.

Audit hash-chain verification is covered by `internal/store` unit tests for valid chains, tamper detection, post-strict mismatch detection even when legacy compatibility is enabled, and PostgreSQL-compatible microsecond timestamp canonicalization.

The running PostgreSQL dev database was verified with legacy compatibility because it contains rows written before stable audit canonicalization:

```json
{
  "valid": true,
  "total": 99,
  "legacy": 76,
  "hashed": 56,
  "warnings_count": 33
}
```

Fresh audit rows written after restarting the dev API use the stable canonicalization path and verify strictly after the legacy prefix. Strict `mcxctl audit-verify` remains the default for fresh environments. The compatibility flag preserves reviewability of older dev rows while still checking `prev_hash` chain links; once a strictly recomputable row appears, later mismatches remain verification errors.

`mcxctl audit-seal` was verified against the same PostgreSQL dev database and produced a WORM/SIEM handoff bundle containing `audit.jsonl`, `manifest.json`, and `manifest.sha256`:

```json
{
  "output": ".data/audit-seals/verify-20260626T065000Z",
  "entry_count": 99,
  "audit_sha256": "1b0ecf0459388fb1d13e9c852994e29422a4e037cb8ce471e9dcf8fad3e0d8ae",
  "manifest_sha256": "52d9abe572c3cb117f15a8e6b83be15e4cc70394d8bdc1b52781280a33ef9739",
  "valid": true,
  "total": 99,
  "hashed": 56,
  "legacy": 76,
  "warnings": 33
}
```

Unit coverage verifies that `audit-seal` creates all bundle files, exports JSONL audit entries, writes manifest hashes, and refuses non-empty output directories.

`mcxctl audit-ship` was verified against the same seal bundle:

```json
{
  "destination": ".data/audit-ship/verify-20260626T065000Z",
  "entry_count": 99,
  "status": "shipped",
  "audit_sha256": "1b0ecf0459388fb1d13e9c852994e29422a4e037cb8ce471e9dcf8fad3e0d8ae",
  "manifest_sha256": "52d9abe572c3cb117f15a8e6b83be15e4cc70394d8bdc1b52781280a33ef9739"
}
```

Unit coverage verifies that `audit-ship` recomputes bundle hashes, rejects tampered audit JSONL, copies with exclusive destination semantics, writes `receipt.json`, rejects repeated shipment to the same target, and posts multipart bundles to HTTP/S collectors.

Unit coverage verifies the S3 Object Lock connector with an `httptest` S3-compatible endpoint. The test ships a sealed bundle to `s3://audit-bucket/compliance`, confirms four `PUT` requests for the three bundle files plus `receipt.json`, checks AWS Signature V4 `Authorization`, `If-None-Match: *`, and S3 Object Lock headers, and verifies the receipt reports `immutable_target:"s3_object_lock"`.

Unit coverage also verifies the API scheduled audit ship path:

- scheduled ship reads the full audit chain through `ListAuditLogsForSeal`
- hash-chain verification runs before any bundle is shipped
- a verified bundle is written under `MULTICODEX_AUDIT_SEAL_ROOT`
- the bundle is copied to the configured `file://` target with a `receipt.json`
- `api.audit_ship` records trigger, redacted target metadata, manifest summary, receipt summary, and verification summary
- missing ship target records `api.audit_ship_failed` and does not create a seal directory

Live scheduler smoke used a temporary in-memory API on `:18082` with `MULTICODEX_AUDIT_SHIP_INTERVAL=1s` and `MULTICODEX_QUEUE_ENABLED=false`. The API emitted repeated `api.audit_ship` audit rows with `trigger:"scheduled"`, `verification.valid:true`, and `receipt.status:"shipped"`. It wrote timestamped bundles under `.data/audit-ship-scheduler-live/seals/` and copied each verified bundle to `.data/audit-ship-scheduler-live/target/` with `receipt.json`.

## Blocker Verification

Verified that each gate blocks later workflow actions:

```json
{
  "scope_violation": "scope violation blocks test, audit, and git sync",
  "test_failed": "test failed or was blocked",
  "audit_blocker": "audit blocker present",
  "approval_rejected": "approval rejected"
}
```

## Remaining Risks

- Docker mode now has a fixed worker image containing Codex CLI `0.142.2`; actual AI execution requires a network-enabled task/profile plus allowlisted deployment secret names such as `OPENAI_API_KEY`. Worker secrets and Git Sync provider tokens can resolve through env/file/Vault providers.
- Scheduler capacity now prevents overcommit, worker timeouts release capacity, capacity pressure persists queued runs, failed workers can enqueue retries according to Agent Profile policy, and multi-node selection is capacity-normalized with rank/utilization analytics.
- SSH executor uses verified HTTP agentd dispatch with optional Bearer-token protection and real forced-command SSH stdin/stdout transport. Production deployments still need hardened key distribution and network segmentation.
- Authentication supports local dev mode plus OIDC RS256/JWKS mode for API and MCP. Group-to-role and group-to-organization mapping, organization provisioning, authorization-code login with PKCE, browser sessions, back-channel logout, server-side token revocation, and expired revocation/login-state cleanup are implemented.
- Metrics support JSON snapshots, Prometheus text format, OpenTelemetry-compatible JSON export, request/run duration histograms, and optional OTLP push to an external collector.
