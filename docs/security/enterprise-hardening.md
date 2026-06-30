# Enterprise Hardening

## Implemented Skeleton

- Local user and membership seed.
- Role to permission mapping in the store layer.
- API RBAC middleware that maps routes to broad permissions and emits denied decisions to `audit_logs`.
- Organization provisioning API, MCP tools, and Web Console view for mapped OIDC organizations.
- Task Envelope validation.
- Resource-aware policy validation:
  - repository must exist
  - Skill must exist and be enabled
  - Agent Profile role and executor must match the task
  - executor node must be available
- MCP Origin guard for local Streamable HTTP usage.
- Request metrics with trace IDs on API and MCP Gateway.
- SSH executor host-key verification state.
- SSH `worker-agentd --forced-command` mode.
- SSH forced-command transport uses `BatchMode=yes`, `StrictHostKeyChecking=yes`, optional configured private key, and optional configured known-hosts file.
- Optional Bearer-token protection for HTTP worker-agentd run intake and remote result/log collection.
- Audit log records for important API and MCP decisions.
- Worker audit records for local, Docker, SSH, timeout, error, and secret environment decisions.
- Worker command, resource, network, dependency, and scope policy decisions with run events and audit records.
- Queue audit records for capacity enqueue, queue dispatch, dispatch failure, dispatch capacity block, and worker retry enqueue decisions.
- Postgres-backed audit hash chain for new audit rows, serialized with a transaction advisory lock.
- `mcxctl audit-verify` for offline audit hash-chain verification against PostgreSQL.
- `mcxctl audit-seal` for verified WORM/SIEM handoff bundles.
- Artifact content read audit logs, including trace id, artifact kind, path, content type, and truncation status.
- Structured secret redaction for executor error output, common token formats, private keys, and keyword assignment patterns.
- Configured worker secret value redaction for Docker worker logs, result files, diffs, and Docker output events.
- Scoped Docker worker secret-env injection from Agent Profile `config.worker_secret_env` plus deployment allowlist.
- Git Sync provider token resolution through env, file, or Vault sources without persisting token values.
- Human approval gates before PR preparation and PR publish operations.

Queue-related action names include:

- `api.worker_enqueue`
- `api.queue_dispatch`
- `api.queue_dispatch_blocked`
- `api.queue_dispatch_failed`
- `api.queue_dispatch_manual`
- `mcp.worker_enqueue`
- `mcp.queue_dispatch`
- `mcp.queue_dispatch_blocked`
- `mcp.queue_dispatch_failed`
- `worker.retry_queued`
- `worker.retry_enqueue_failed`

## RBAC Direction

Current roles:

- `owner`
- `admin`
- `tech_lead`
- `reviewer`
- `operator`
- `auditor`
- `viewer`

The default local API runs as `local-dev`, but requests pass through permission checks. When `MULTICODEX_AUTH_MODE=oidc`, API requests under `/api/v1/` require a Bearer JWT validated with RS256 against a configured or discovered JWKS endpoint.

## SSO / OIDC Surface

The data model already includes:

- `users`
- `memberships`
- organization-scoped records

Configuration:

```bash
MULTICODEX_AUTH_MODE=oidc
MULTICODEX_OIDC_ISSUER=https://issuer.example
MULTICODEX_OIDC_AUDIENCE=multi-codex
MULTICODEX_OIDC_JWKS_URL=https://issuer.example/.well-known/jwks.json
MULTICODEX_OIDC_CLIENT_ID=multi-codex-web
MULTICODEX_OIDC_CLIENT_SECRET=
MULTICODEX_OIDC_CLIENT_AUTH_METHOD=client_secret_post
MULTICODEX_OIDC_REDIRECT_URL=https://multi-codex.example/api/v1/auth/callback
MULTICODEX_OIDC_AUTHORIZATION_URL=
MULTICODEX_OIDC_TOKEN_URL=
MULTICODEX_OIDC_POST_LOGIN_REDIRECT_URL=/
MULTICODEX_OIDC_DEFAULT_ROLE=viewer
MULTICODEX_OIDC_DEFAULT_ORG_ID=
MULTICODEX_OIDC_GROUP_ROLE_MAP=engineering=operator;security=auditor
MULTICODEX_OIDC_GROUP_ORG_MAP=engineering=00000000-0000-7000-8000-000000000001
```

Implemented behavior:

- API and MCP local mode remain frictionless for Docker development.
- OIDC mode fails closed when the Authorization header is absent or invalid.
- JWT signature, issuer, audience, expiry, and not-before claims are validated.
- Ordered group-to-role and group-to-organization mappings can bind IdP groups to local RBAC and membership scope.
- Exact role claims or group claims such as `multi-codex:operator` map to RBAC permissions.
- API authentication failures emit `api.auth_denied` audit records.
- MCP authentication failures emit `mcp.auth_denied` audit records.
- Successful API OIDC mapping emits `api.auth_oidc_mapped` audit records.
- Successful MCP OIDC mapping emits `mcp.auth_oidc_mapped` audit records.
- Human API audit records bind to the authenticated request user when available.
- MCP tool audit records bind to the authenticated subject when available.
- OIDC subjects are persisted to `users.external_provider` and `users.external_subject`.
- OIDC users are upserted into mapped organization `memberships` with mapped RBAC roles.
- Organizations can be provisioned through audited API requests or MCP tools before assigning IdP groups to their IDs.
- The Web Console `Sign in` action starts an OIDC authorization-code flow with PKCE and nonce validation through `GET /api/v1/auth/login` and `GET /api/v1/auth/callback`.
- Login state is persisted in `auth_login_states` with a hashed state token, hashed nonce, short expiry, one-time consumption, and an unlogged short-lived PKCE verifier.
- The bearer-token `Connect` path remains available for operator/debug workflows and exchanges a valid token through `POST /api/v1/auth/session` before clearing the browser token draft.
- `POST /api/v1/auth/logout` is idempotent and writes an `api.auth_logout` audit row with the authenticated human actor when available, or `anonymous` when the caller has no valid auth context.
- OIDC logout stores a SHA-256 token hash in `auth_token_revocations`; the raw bearer token is never persisted.
- API and MCP OIDC authentication reject active tokens whose hashes are in the revocation table and emit `api.auth_denied` or `mcp.auth_denied`.
- `POST /api/v1/auth/session` exchanges a valid OIDC bearer token for an opaque HttpOnly browser session cookie. The server stores only the session token hash in `auth_sessions`; browser requests can then authenticate with the cookie instead of long-lived `localStorage` bearer storage.
- Authorization-code callback also creates the same opaque HttpOnly browser session and records the upstream OIDC `sid` when present.
- Browser session cookies use `HttpOnly`, `SameSite=Lax`, `Path=/`, and `MULTICODEX_AUTH_COOKIE_SECURE` for HTTPS deployments.
- `POST /api/v1/auth/backchannel/logout` verifies OIDC logout tokens, requires the back-channel logout event claim, and revokes sessions by upstream `sid` or by subject fallback.
- Logout revokes the current browser session hash and clears the cookie.
- Retention cleanup removes expired token revocation rows and reports counts in the `auth_token_revocations` result section.
- Retention cleanup removes expired or revoked browser session rows and reports counts in the `auth_sessions` result section.
- Retention cleanup removes expired or consumed login states and reports counts in the `auth_login_states` result section.

Remaining production integration work:

- Register the final redirect URL with the enterprise IdP. `none`, `client_secret_post`, and `client_secret_basic` token endpoint authentication are implemented; provider-specific private-key JWT or mTLS client authentication would be an additional deployment integration.

## Worker Runtime Secrets

Worker secrets use references, not plaintext database values. A profile may request names only:

```json
{
  "worker_secret_env": ["OPENAI_API_KEY", "CODEX_AUTH_TOKEN"]
}
```

Deployment config must separately allow those names and choose where values are resolved:

```bash
MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST=OPENAI_API_KEY,CODEX_AUTH_TOKEN
MULTICODEX_WORKER_SECRET_PROVIDER=env
```

The default `env` provider reads the API process environment. The `file` provider reads a flat JSON secret file on each worker start:

```bash
MULTICODEX_WORKER_SECRET_PROVIDER=file
MULTICODEX_WORKER_SECRET_FILE_PATH=/run/secrets/multi-codex-worker.json
```

The `vault` provider reads HashiCorp Vault KV v2:

```bash
MULTICODEX_WORKER_SECRET_PROVIDER=vault
MULTICODEX_WORKER_VAULT_ADDR=https://vault.example
MULTICODEX_WORKER_VAULT_TOKEN_FILE=/run/secrets/vault-token
MULTICODEX_WORKER_VAULT_NAMESPACE=
MULTICODEX_WORKER_VAULT_MOUNT=kv
MULTICODEX_WORKER_VAULT_SECRET_PATH=multi-codex/worker
```

The Docker executor injects an allowed env name only when the task envelope also enables network. Skipped decisions are recorded with reasons such as `network_disabled`, `not_allowlisted`, `missing_host_env`, `missing_secret`, `secret_provider_error`, or `invalid_name`. Secret values are never written to audit payloads.

## Git Sync Provider Credentials

Git Sync does not store provider tokens in PostgreSQL. Live PR creation resolves `GITHUB_TOKEN` or `GITLAB_TOKEN` at publish time. Existing direct process environment variables still work for local compatibility:

```bash
GITHUB_TOKEN=ghp_...
GITLAB_TOKEN=glpat-...
```

The preferred deployment path uses the same resolver family as worker secrets:

```bash
MULTICODEX_GIT_CREDENTIAL_PROVIDER=file
MULTICODEX_GIT_CREDENTIAL_FILE_PATH=/run/secrets/multi-codex-git.json
```

The file provider expects a flat JSON object:

```json
{
  "GITHUB_TOKEN": "ghp_...",
  "GITLAB_TOKEN": "glpat-..."
}
```

Vault KV v2 is configured independently from worker secrets so deployments can separate Codex runtime credentials from Git provider credentials:

```bash
MULTICODEX_GIT_CREDENTIAL_PROVIDER=vault
MULTICODEX_GIT_VAULT_ADDR=https://vault.example
MULTICODEX_GIT_VAULT_TOKEN_FILE=/run/secrets/git-vault-token
MULTICODEX_GIT_VAULT_NAMESPACE=
MULTICODEX_GIT_VAULT_MOUNT=kv
MULTICODEX_GIT_VAULT_SECRET_PATH=multi-codex/git
```

API and MCP `git_publish_pr` events record `credential_provider` and `credential_resolved`. Provider response errors are redacted before they are returned or persisted. Auto-merge remains disabled.

Production live PR creation is fail-closed behind the pilot review flag:

```bash
MULTICODEX_GIT_SYNC_MODE=live
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true
```

Without that explicit review acknowledgement, API and MCP Gateway refuse to
start in production live mode. The workflow still requires the task-level
`pr_publish` approval before any provider PR call is made.

## Artifact Retention

Artifacts store metadata, filesystem paths, and hashes where available. The API exposes textual artifact content through `GET /api/v1/artifacts/{artifact_id}/content` with a 2 MiB response cap and emits an `api.artifact_read` audit record for successful reads. Memory-backed artifacts, such as prepared PR bodies, are read from artifact metadata. Web Console live run events use `GET /api/v1/runs/{run_id}/events/stream`; stream lifecycle decisions emit `api.run_event_stream_open` and `api.run_event_stream_close`.

Retention cleanup is available through the CLI:

```bash
mcxctl retention-cleanup -dry-run=true -max-age=720h
```

Retention cleanup can also run as an opt-in API background worker:

```bash
MULTICODEX_RETENTION_ENABLED=true
MULTICODEX_RETENTION_INTERVAL=1h
MULTICODEX_RETENTION_MAX_AGE=720h
MULTICODEX_RETENTION_DRY_RUN=true
```

The cleanup command and worker scan run, artifact, and worktree roots. They also clean expired persisted MCP replay sessions/events, bearer-token revocations, browser sessions, and one-time OIDC login states. They report scanned/deleted counts, reclaimed filesystem bytes, and database retention counts. Scheduled worker decisions emit `api.retention_cleanup` or `api.retention_cleanup_failed` audit rows.

Retention policy should:

- keeps audit-critical `task.json`, `prompt.md`, `result.json`, and `diff.patch`
- expires bulky logs after a configured retention period
- expires MCP replay events after the configured retention period
- expires consumed or expired OIDC login states after the configured retention period
- records cleanup decisions in `audit_logs`

## Backup and Restore

Minimum backup set:

- PostgreSQL database
- artifact root
- run root
- repo cache, if local mirrors should survive restore

Restore order:

1. Restore PostgreSQL.
2. Restore artifact and run roots.
3. Rebuild repo cache if it was not restored.
4. Run `mcxctl migrate`.
5. Start API/MCP/Web services.

Operational commands:

```bash
mcxctl backup -output .data/backups/$(date -u +%Y%m%dT%H%M%SZ)
mcxctl restore -input .data/backups/<backup-dir>
```

`backup` writes a manifest, archives run/artifact/worktree roots, and uses `pg_dump` when it is available. `restore` uses `psql` when a database dump exists and restores filesystem archives.

## MCP Session Lifecycle

The MCP Gateway tracks Streamable HTTP sessions with an in-process TTL:

```bash
MULTICODEX_MCP_SESSION_TTL=8h
```

Each MCP response includes `MCP-Session-Expires-At`. Active POST requests and SSE streams extend the deadline. Expired sessions return `404` and emit `mcp.session_expired` audit rows. SSE streams include event `id:` fields; reconnects with `Last-Event-ID` emit `mcp.session_resume` and `mcp.session_stream_open` rows with resume metadata. PostgreSQL-backed deployments publish committed session events through `LISTEN/NOTIFY`; active streams read the durable event rows before sending data and fall back to interval polling. Notification fanout emits `mcp.session_notify_fanout`; subscription failures emit `mcp.session_notify_subscribe_failed` and continue polling.

PostgreSQL-backed deployments provide durable shared replay storage for gateway restarts and load-balanced instances. Larger deployments can still introduce Redis Streams or NATS if they need a dedicated high-throughput fanout bus.

## worker-agentd Authentication

Set `MULTICODEX_AGENTD_TOKEN` on API, MCP Gateway, and worker-agentd to protect the HTTP agentd transport:

```bash
MULTICODEX_AGENTD_TOKEN=replace-with-a-deployment-secret
```

When configured, agentd rejects unauthenticated run creation and result/log retrieval with `401`; the executor automatically sends the Bearer token. `GET /healthz` is intentionally left open for local and container health checks. SSH forced-command transport remains protected by SSH host-key verification, deployment keys, `StrictHostKeyChecking=yes`, and the node `forced_command` setting rather than this HTTP token.

## Metrics and Tracing

Implemented now:

- `/metrics` on API.
- `/metrics` on MCP Gateway.
- Prometheus text format through `?format=prometheus` or text `Accept` headers.
- OpenTelemetry-compatible JSON metric export through `?format=otlp` or `Accept: application/x-otlp-json`.
- Dynamic URL segment normalization for metrics labels.
- Store-derived run count, active-run, and run-duration metrics grouped by role, executor, and status.
- Operational Prometheus metrics for queue depth, worker terminal failures, audit ship, retention cleanup, and telemetry push failures.
- Prometheus and OTLP-compatible histogram buckets for HTTP request duration and completed worker run duration.
- Optional OTLP JSON push from API and MCP Gateway through `MULTICODEX_TELEMETRY_PUSH_URL`.
- `X-Multi-Codex-Trace-Id` response header.
- W3C `traceparent` request intake and response propagation.

Failed telemetry push attempts emit `api.telemetry_push_failed` or `mcp.telemetry_push_failed` audit rows. Production deployments should still place collectors behind trusted network policy and TLS.

## Audit Hash Chain

New audit rows include:

- `prev_hash`
- `entry_hash`

The hash input covers actor, action, resource, payload, previous hash, and creation timestamp. Existing rows created before `000004_audit_hash_chain.sql` may have empty hash fields; the first post-migration row starts a new chain segment, and subsequent rows point to the previous `entry_hash`.

Verify the database chain with:

```bash
mcxctl audit-verify
```

The verifier recomputes each hash, checks every `prev_hash` link, reports the first and last hash, and exits non-zero when the strict chain is invalid. The canonical timestamp is truncated to microsecond precision before hashing so PostgreSQL `timestamptz` round trips do not create false mismatches.

Development databases that already contain rows from before the stable canonicalization can be inspected with:

```bash
mcxctl audit-verify -allow-legacy-hash-mismatch=true
```

That compatibility flag turns only the leading pre-stable hash mismatches into warnings while still checking chain links. After the first strictly recomputable hash is seen, later mismatches remain hard verification errors.

Production deployments should still export audit logs to append-only object storage or a SIEM so the database chain can be independently retained.

Create a sealed handoff bundle with:

```bash
mcxctl audit-seal -output .data/audit-seals/$(date -u +%Y%m%dT%H%M%SZ)
```

The command verifies the hash chain before writing, refuses to write into a non-empty output directory, and produces `audit.jsonl`, `manifest.json`, and `manifest.sha256`. The manifest records the JSONL SHA256, verification summary, first/last audit IDs, entry count, and bundle format `multi-codex.audit-seal.v1`. The local directory is a staging area for external WORM storage or SIEM ingestion.

Ship a verified bundle to a mounted WORM/SIEM ingress directory with:

```bash
mcxctl audit-ship -input .data/audit-seals/<bundle> -target file:///mnt/worm/multi-codex
```

`audit-ship` recomputes the manifest hash, verifies `manifest.sha256`, recomputes the audit JSONL hash, and rejects unsupported bundle formats. For `file://` targets it creates the destination bundle directory with exclusive semantics, copies the three bundle files without overwrite, and writes `receipt.json` with the shipped hashes and destination. For `http://` or `https://` targets it posts a multipart payload containing the three bundle files plus hash metadata for generic SIEM collectors. For `s3://bucket/prefix` targets it uploads `audit.jsonl`, `manifest.json`, `manifest.sha256`, and `receipt.json` with AWS Signature V4, `If-None-Match: *`, and optional S3 Object Lock headers. `MULTICODEX_AUDIT_SHIP_TARGET` can provide a default target.

S3 Object Lock handoff uses:

```bash
MULTICODEX_AUDIT_SHIP_TARGET=s3://audit-bucket/multi-codex
MULTICODEX_AUDIT_SHIP_S3_REGION=us-east-1
MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_MODE=COMPLIANCE
MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_RETAIN_UNTIL=2030-01-01T00:00:00Z
MULTICODEX_AUDIT_SHIP_S3_OBJECT_LOCK_LEGAL_HOLD=ON
AWS_ACCESS_KEY_ID=...
AWS_SECRET_ACCESS_KEY=...
```

The bucket must already have S3 Object Lock enabled and an appropriate retention policy. `MULTICODEX_AUDIT_SHIP_S3_ENDPOINT` can point tests or compatible object stores at a custom endpoint.

The API can also run an opt-in scheduled seal-and-ship worker:

```bash
MULTICODEX_AUDIT_SHIP_ENABLED=true
MULTICODEX_AUDIT_SHIP_INTERVAL=24h
MULTICODEX_AUDIT_SEAL_ROOT=/var/lib/multi-codex/audit-seals/scheduled
MULTICODEX_AUDIT_SHIP_TARGET=file:///mnt/worm/multi-codex
```

The worker is disabled by default. Each scheduled run reads the full audit chain, verifies hashes before writing a bundle, ships only a verified bundle, and records `api.audit_ship` or `api.audit_ship_failed`. The audit payload records a redacted target descriptor, verification summary, manifest hashes, receipt hashes, output directory, and error details when blocked. Set `MULTICODEX_AUDIT_SHIP_ALLOW_LEGACY_HASH_MISMATCH=true` only for local/dev databases with a known legacy canonicalization prefix; strict verification is the production default.

## Audit Export

Set `MULTICODEX_AUDIT_EXPORT_PATH` to mirror every audit row to an append-only JSONL file:

```bash
MULTICODEX_AUDIT_EXPORT_PATH=/var/lib/multi-codex/audit/audit.jsonl
```

Each exported line contains the same `prev_hash` and `entry_hash` fields as the database record. For stronger immutability, mount this path on WORM storage or ship it with a log collector that preserves append-only semantics.
