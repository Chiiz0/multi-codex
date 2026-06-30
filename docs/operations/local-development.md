# Local Development Operations

## Start

```bash
cp .env.example .env
make dev-image
make dev-up
```

Local Docker development uses PostgreSQL trust auth and a passwordless database URL. Leave `POSTGRES_PASSWORD` empty for this stack; set it only when testing the production-style Compose file.

## Endpoints

- API: http://localhost:8080/healthz
- MCP Gateway façade: http://localhost:8090/healthz
- Web console: http://localhost:3000
- worker-agentd: http://localhost:7070/healthz
- PostgreSQL: `localhost:5432`

Local development uses `MULTICODEX_AUTH_MODE=local`, which returns the seeded owner identity at `/api/v1/auth/me`.

The Web Console sidebar includes a Session control. In local mode it shows the seeded identity. In OIDC mode, `Sign in` starts the authorization-code flow through `/api/v1/auth/login` and `/api/v1/auth/callback`; the API validates PKCE and nonce, then sets an opaque HttpOnly browser session cookie. The `Connect` button remains available for operator-provided bearer tokens and exchanges them through `POST /api/v1/auth/session` before clearing the browser token draft. `Sign out` calls `POST /api/v1/auth/logout`, clears the browser token/cookie, writes an `api.auth_logout` audit row, records the OIDC bearer-token hash in the server-side revocation table when a bearer is present, and revokes the browser session hash when a session cookie is present.

The logout endpoint can also be tested directly:

```bash
curl -s -X POST http://localhost:8080/api/v1/auth/logout
```

To smoke-test fail-closed OIDC mode without changing the Docker dev stack:

```bash
MULTICODEX_API_LISTEN=:18080 \
MULTICODEX_DATABASE_URL='' \
MULTICODEX_AUTH_MODE=oidc \
MULTICODEX_OIDC_ISSUER=https://issuer.example \
MULTICODEX_OIDC_AUDIENCE=multi-codex \
MULTICODEX_OIDC_JWKS_URL=http://127.0.0.1:9/jwks \
go run ./cmd/api
```

Then:

```bash
curl -i http://localhost:18080/api/v1/auth/me
```

Expected status: `401 Unauthorized`.

## MCP Sessions

The MCP Gateway defaults to an 8 hour session TTL:

```bash
MULTICODEX_MCP_SESSION_TTL=8h
MULTICODEX_MCP_LIVE_FANOUT_INTERVAL=1s
```

Initialize responses include `MCP-Session-Id` and `MCP-Session-Expires-At`. Subsequent valid POST requests or SSE streams extend the deadline. Expired sessions return `404`. SSE ready and heartbeat messages are persisted to `mcp_session_events`, so reconnecting `GET /mcp` with `Accept: text/event-stream`, the same `MCP-Session-Id`, and a `Last-Event-ID` header replays missed events before appending a new ready event. With PostgreSQL storage, active SSE streams subscribe to `LISTEN/NOTIFY` fanout and then read the durable event rows; they also poll persisted session events every `MULTICODEX_MCP_LIVE_FANOUT_INTERVAL` as fallback. This lets one gateway replica deliver events appended by another replica without waiting for client reconnect. The gateway emits `mcp.session_resume`, `mcp.session_stream_open`, `mcp.session_notify_fanout`, and `mcp.session_fanout` audit rows with event cursor metadata.

## Database Migration

```bash
make migrate-dev
```

`make migrate-dev` starts Postgres 18, waits for health, and runs `mcxctl migrate`.

## Backend Verification

```bash
make backend-test
make backend-build
```

## Frontend Verification

```bash
make frontend-install
make frontend-build
```

These commands run inside the fixed dev container.

## End-to-End Local Check

```bash
make dev-up
```

Then open http://localhost:3000, create a task with `New Task`, and run it with `Start`.

Expected run events:

```text
worker_spawn
workspace_seed_repo_bootstrap
executor_prepare
worker_log
worker_result
```

`workspace_seed_repo_bootstrap` appears on the first run when the seeded local `demo-service.git` remote does not exist yet. The executor creates a real bare Git repository, clones it into the run workspace, and records the bootstrap in run events and audit logs.

The Web Console Run Detail uses `GET /api/v1/runs/{run_id}/events/stream` as a Server-Sent Events source for live run events and falls back to polling `GET /api/v1/runs/{run_id}/events` if the stream is unavailable. Stream opens and closes emit `api.run_event_stream_open` and `api.run_event_stream_close`.

## Queue and Retry Checks

The API queue dispatcher is enabled by default:

```bash
MULTICODEX_QUEUE_ENABLED=true
MULTICODEX_QUEUE_DISPATCH_INTERVAL=5s
```

Inspect queue state:

```bash
curl http://localhost:8080/api/v1/queue
```

The response includes Docker and SSH backpressure snapshots with per-node utilization, available slots, and selection rank:

```bash
curl -s http://localhost:8080/api/v1/queue | jq '.backpressure.docker.nodes'
```

Dispatch one queued run manually:

```bash
curl -X POST http://localhost:8080/api/v1/queue/dispatch
```

MCP exposes the same controls through `queue_status` and `queue_dispatch`.

Agent Profiles can tune scheduling:

```json
{
  "queue_priority": 10,
  "retry_max_attempts": 2
}
```

Failed workers enqueue the next attempt until the configured maximum is reached. Queue enqueue, dispatch, blocked dispatch, and retry decisions are recorded in `audit_logs`.

## Docker Worker Credentials

Docker mode does not inherit arbitrary API environment variables. To let a network-enabled worker use Codex credentials, set the env var on the API process, allowlist its name, and request the same name from the Agent Profile:

```bash
MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST=OPENAI_API_KEY,CODEX_AUTH_TOKEN
MULTICODEX_WORKER_SECRET_PROVIDER=env
```

The profile config should contain:

```json
{
  "worker_secret_env": ["OPENAI_API_KEY"]
}
```

The task envelope must also set `network=true`, and the selected Agent Profile must have `network_enabled=true`.

For a Docker/Kubernetes-style secret file, use:

```bash
MULTICODEX_WORKER_SECRET_PROVIDER=file
MULTICODEX_WORKER_SECRET_FILE_PATH=/run/secrets/multi-codex-worker.json
```

The file provider expects a flat JSON object and reloads it for each worker start:

```json
{
  "OPENAI_API_KEY": "sk-..."
}
```

For Vault KV v2, use:

```bash
MULTICODEX_WORKER_SECRET_PROVIDER=vault
MULTICODEX_WORKER_VAULT_ADDR=https://vault.example
MULTICODEX_WORKER_VAULT_TOKEN_FILE=/run/secrets/vault-token
MULTICODEX_WORKER_VAULT_MOUNT=kv
MULTICODEX_WORKER_VAULT_SECRET_PATH=multi-codex/worker
```

The provider reads `/v1/<mount>/data/<path>` and resolves the requested `worker_secret_env` names from the returned `data.data` object.

## Git Sync Credentials

Git Sync dry-run mode needs no provider token:

```bash
MULTICODEX_GIT_SYNC_MODE=dry-run
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=false
```

Live provider calls are opt-in and still require the `pr_publish` approval gate. For local compatibility, set provider tokens on the API/MCP process:

```bash
MULTICODEX_GIT_SYNC_MODE=live
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true
GITHUB_TOKEN=ghp_...
GITLAB_TOKEN=glpat-...
```

For file-backed deployment secrets, use:

```bash
MULTICODEX_GIT_CREDENTIAL_PROVIDER=file
MULTICODEX_GIT_CREDENTIAL_FILE_PATH=/run/secrets/multi-codex-git.json
```

The file provider expects a flat JSON object and reloads it for each publish attempt:

```json
{
  "GITHUB_TOKEN": "ghp_...",
  "GITLAB_TOKEN": "glpat-..."
}
```

For Vault KV v2, use:

```bash
MULTICODEX_GIT_CREDENTIAL_PROVIDER=vault
MULTICODEX_GIT_VAULT_ADDR=https://vault.example
MULTICODEX_GIT_VAULT_TOKEN_FILE=/run/secrets/git-vault-token
MULTICODEX_GIT_VAULT_MOUNT=kv
MULTICODEX_GIT_VAULT_SECRET_PATH=multi-codex/git
```

Git Sync resolves only the provider token name required by the target repository provider. API and MCP audit rows record provider metadata and resolved status, not token values. Provider error bodies are redacted before persistence.

## Agentd Check

```bash
docker compose -f deployments/docker/compose.dev.yaml up -d worker-agentd-dev
curl http://localhost:7070/healthz
curl http://localhost:7070/v1/runs \
  -H 'Content-Type: application/json' \
  -d '{"run_id":"agentd_verify_1","task_id":"task_verify","role":"feature","prompt":"verify"}'
curl http://localhost:7070/v1/runs/agentd_verify_1/result
```

Set `MULTICODEX_AGENTD_TOKEN` on API, MCP Gateway, and worker-agentd to require Bearer auth for `POST /v1/runs` and remote result/log retrieval:

```bash
MULTICODEX_AGENTD_TOKEN=dev-agentd-secret docker compose -f deployments/docker/compose.dev.yaml up -d api-dev mcp-gateway-dev worker-agentd-dev
curl -i http://localhost:7070/v1/runs/agentd_verify_1/logs
curl -H 'Authorization: Bearer dev-agentd-secret' http://localhost:7070/v1/runs/agentd_verify_1/logs
```

Forced-command mode:

```bash
printf '{"run_id":"forced_verify","task_id":"task_forced","role":"feature","prompt":"verify"}\n' \
  | MULTICODEX_RUN_ROOT="$(mktemp -d)" go run ./cmd/worker-agentd --forced-command
```

For real SSH transport, register an SSH executor node with `address`, `host_key_fingerprint`, `forced_command`, and `host_key_verified=true`, leaving `agentd_url` empty. The API will send the controlled run JSON over stdin to:

```bash
ssh -T <address> <forced_command>
```

Container deployments can configure:

```bash
MULTICODEX_SSH_PRIVATE_KEY_PATH=/run/secrets/multi-codex-ssh
MULTICODEX_SSH_KNOWN_HOSTS_PATH=/run/secrets/known_hosts
MULTICODEX_SSH_CONNECT_TIMEOUT=15s
```

## Metrics

```bash
curl http://localhost:8080/metrics
curl http://localhost:8090/metrics
curl http://localhost:8080/metrics?format=otlp
```

Responses include request counters, error counters, last status, last trace ID, run metrics, request/run duration histograms, Prometheus text output, and OpenTelemetry-compatible JSON output.

Optional OTLP push is disabled by default. To send the same OTLP JSON payload to a collector:

```bash
MULTICODEX_TELEMETRY_PUSH_URL=http://collector.example/v1/metrics
MULTICODEX_TELEMETRY_PUSH_INTERVAL=1m
```

API and MCP Gateway push independently. Failed push attempts emit `api.telemetry_push_failed` or `mcp.telemetry_push_failed` audit rows.

## Retention and Backup

```bash
go run ./cmd/mcxctl retention-cleanup -dry-run=true -max-age=720h
go run ./cmd/mcxctl backup -output .data/backups/manual
go run ./cmd/mcxctl audit-seal -output .data/audit-seals/manual
go run ./cmd/mcxctl audit-ship -input .data/audit-seals/manual -target file://.data/audit-ship
go run ./cmd/mcxctl pilot-verify -task-id <pilot-task-id> -strict=false
```

`retention-cleanup` returns the original filesystem cleanup fields, an `mcp_sessions` section with dry-run/delete counts for expired persisted MCP replay sessions and events, an `auth_token_revocations` section with counts for expired logout denylist rows, an `auth_sessions` section with counts for expired or revoked browser session rows, and an `auth_login_states` section with counts for expired or consumed authorization-code login state rows.

Service-side retention is disabled by default. To verify the scheduler without deleting files, run a temporary API with:

```bash
MULTICODEX_API_LISTEN=:18081 \
MULTICODEX_DATABASE_URL='' \
MULTICODEX_RETENTION_ENABLED=true \
MULTICODEX_RETENTION_INTERVAL=1s \
MULTICODEX_RETENTION_MAX_AGE=1h \
MULTICODEX_RETENTION_DRY_RUN=true \
go run ./cmd/api
```

Then query:

```bash
curl http://localhost:18081/api/v1/audit-logs
```

## Audit Integrity

Verify the PostgreSQL audit hash chain:

```bash
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' \
  go run ./cmd/mcxctl audit-verify
```

Older dev databases may contain audit rows written before the stable timestamp canonicalization. For those local databases, run:

```bash
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' \
  go run ./cmd/mcxctl audit-verify -allow-legacy-hash-mismatch=true
```

The compatibility mode should be treated as a migration/debugging tool. It still validates `prev_hash` links and reports warnings, while strict mode remains the default for fresh environments.

Create a sealed audit handoff bundle:

```bash
MULTICODEX_DATABASE_URL='postgres://multi_codex@localhost:5432/multi_codex?sslmode=disable' \
  go run ./cmd/mcxctl audit-seal -allow-legacy-hash-mismatch=true -output .data/audit-seals/manual
```

The output directory must be empty. The bundle contains `audit.jsonl`, `manifest.json`, and `manifest.sha256`.

Ship a sealed bundle to a local WORM/SIEM ingress directory:

```bash
go run ./cmd/mcxctl audit-ship -input .data/audit-seals/manual -target file://.data/audit-ship
```

The command verifies `manifest.sha256` and `audit_sha256`, refuses to overwrite an existing file destination, and writes `receipt.json` next to the shipped bundle files. HTTP/S targets receive a multipart payload with the bundle files and hash metadata. S3 targets use `s3://bucket/prefix`, AWS Signature V4, `If-None-Match: *`, and optional S3 Object Lock headers from `MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_*`. Set `MULTICODEX_AUDIT_SHIP_TARGET=file://.data/audit-ship` or `s3://audit-bucket/multi-codex` to use a default target.

The API scheduled audit ship worker is disabled by default. To verify it locally without changing the main dev API, run a temporary API on a different port with an isolated in-memory store:

```bash
rm -rf .data/audit-ship-scheduler
MULTICODEX_API_LISTEN=:18082 \
MULTICODEX_DATABASE_URL='' \
MULTICODEX_AUDIT_SHIP_ENABLED=true \
MULTICODEX_AUDIT_SHIP_INTERVAL=1s \
MULTICODEX_AUDIT_SEAL_ROOT=.data/audit-ship-scheduler/seals \
MULTICODEX_AUDIT_SHIP_TARGET=file://.data/audit-ship-scheduler/target \
go run ./cmd/api
```

Then query:

```bash
curl http://localhost:18082/api/v1/audit-logs
```

Each tick writes `api.audit_ship` on success or `api.audit_ship_failed` when verification, bundle writing, or shipping fails. The worker verifies the full audit chain before writing and uses exclusive destination directories, so repeated runs create separate timestamped bundles.

## Verification Report

See [Verification Report](verification-report.md).
