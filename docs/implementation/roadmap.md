# Implementation Roadmap

## Phase 0: Project Initialization

Status: completed for the current MVP slice.

Delivered in the first code slice:

- Repository structure.
- Go module and runnable API/MCP binaries.
- Vite/React web app scaffold.
- Docker Compose files.
- PostgreSQL migration baseline.
- Fixed Docker development image workflow.

## Phase 1: Core Backend and Data Model

Status: completed for the current enterprise MVP slice.

Completed:

- PostgreSQL migration baseline.
- Idempotent migration runner.
- PostgreSQL-backed storage for projects, repositories, tasks, runs, run events, tool calls, artifacts, approvals, Skills, Agent Profiles, executor nodes, and audit logs.
- In-memory fallback store for fast local development.
- Local auth/RBAC skeleton.
- Organization provisioning API for OIDC membership targets.
- Resource-aware policy validation.

## Phase 2: MCP Gateway MVP

Status: completed for controlled MVP tools.

Completed:

- Streamable HTTP-style `/mcp` endpoint for JSON-RPC `initialize`, `ping`, `tools/list`, and `tools/call`.
- Compatibility HTTP façade for initial MCP-like tools.
- Streamable HTTP conformance coverage for Accept negotiation, SSE GET, notifications, MCP response headers, and Origin guard.
- Generated or client-provided MCP session IDs with audited initialize, notification, and SSE stream-open lifecycle events.
- Configurable MCP session TTL, expired-session rejection, SSE event ids, and audited `Last-Event-ID` resume handling.
- Durable `mcp_sessions` and `mcp_session_events` persistence for replayable SSE ready/heartbeat events across gateway restarts.
- Cross-replica live fanout for active SSE subscribers through PostgreSQL LISTEN/NOTIFY backed by durable session-event reads, with persisted session-event polling as fallback.
- Retention cleanup for old persisted MCP replay sessions and events.
- Tool call and audit log persistence.
- Task, worker, scope, test, audit, approval, and git PR preparation tools.
- Organization list/create tools for enterprise setup through Main Codex.
- Queue status and manual queue dispatch tools backed by persisted run state.
- Local Origin guard for MCP HTTP.

Next work:

- Add Redis Streams or NATS only if a deployment needs a fanout bus beyond PostgreSQL LISTEN/NOTIFY.

## Phase 3: Docker Executor

Status: completed for local verified MVP.

Completed:

- Run directory rendering.
- `task.json`, `prompt.md`, `AGENTS.override.md`, `worker.log`, `result.json`, and `diff.patch` generation.
- Artifact metadata persistence.
- Repo mirror and workspace preparation path.
- Fresh-checkout bootstrap for the seeded local `demo-service.git` remote, with run event and audit records.
- Docker worker mount and Codex CLI invocation path.
- Fixed Codex worker image with pinned `@openai/codex@0.142.2`.
- Policy-gated Docker network mode: `none` by default, `bridge` only when task and Agent Profile allow it.
- Scoped worker secret-env injection through Agent Profile refs, deployment allowlist, and env/file/Vault secret resolvers.
- Worker audit records for local, Docker, SSH, timeout, error, and secret-env decisions.
- Git diff based scope record when changed files exist.
- Retention cleanup policy for old run, artifact, and worktree directories through CLI and opt-in API scheduler.

Next work:

- Continue production executor hardening through Phase 8 controls.

## Phase 4: Skills and Agent Profiles

Status: completed for MVP management.

Completed:

- Skill registration API and Web Console.
- Skill version/hash/path metadata with API and Web Console version-history query.
- Agent Profile create/list API and Web Console.
- Worker prompt rendering uses Task Envelope and role context.

## Phase 5: Workflow State Machine

Status: completed for MVP gates.

Completed:

- feature -> scope -> test -> audit -> approval -> git-sync state evaluation.
- Scope violations block later gates.
- Test failures block later gates.
- Audit blockers block git sync.
- Approval rejection blocks git sync.
- Git Sync prepares PR body material and does not push or merge.

## Phase 6: Web Console

Status: completed for MVP operations.

Completed:

- Dashboard.
- Task Detail.
- Run Detail with live run-event SSE stream, polling fallback, and events/artifacts.
- Scope Check.
- Approval Center.
- Node Management.
- Organization Management.
- Skill/Profile Management.
- Audit Log and MCP Tool Call query.

## Phase 7: SSH Executor

Status: usable remote executor path.

Completed:

- Executor node registration.
- Host key fingerprint storage and verification API.
- `worker-agentd` remote run service.
- agentd health, run intake, log retrieval, and result retrieval.
- Optional Bearer-token protection for HTTP agentd run intake and result/log retrieval.
- `worker-agentd --forced-command` stdin/stdout mode for SSH forced-command wiring.
- API executor dispatch through verified SSH nodes.
- SSH forced-command stdin/stdout transport for nodes with `address` and `forced_command`.
- Remote log/result collection into run artifacts.
- Executor node capacity enforcement for active runs.
- Worker timeout enforcement with Docker timeout cleanup.
- Structured API/MCP backpressure details with retry-after guidance when capacity is full.
- Persisted worker queue with priority ordering.
- API queue dispatcher plus API/MCP manual dispatch controls.
- Automatic retry enqueue for failed workers according to Agent Profile policy.
- Capacity-normalized multi-node scheduling by utilization, available slots, heartbeat freshness, and node age.
- Backpressure analytics with per-node available slots, utilization, selection rank, and selection/ineligibility reasons.

## Phase 8: Security and Enterprise

Status: completed for enterprise MVP controls.

Completed:

- Local RBAC skeleton.
- SSO/OIDC reserved model.
- Secret-keyword redaction in executor output.
- Worker secret resolver abstraction with env, file, and Vault KV v2 providers.
- Git Sync provider credential resolver with env, file, and Vault KV v2 sources.
- Artifact metadata and retention direction.
- Retention cleanup command.
- Opt-in retention cleanup worker scheduling with audited decisions.
- MCP replay session/event retention through the same cleanup command and worker.
- Backup/restore commands.
- Audit hash-chain verification command.
- Audit seal bundle command for verified WORM/SIEM handoff.
- Audit ship command for hash-verified `file://` WORM/SIEM ingress handoff and HTTP/S collector multipart handoff.
- S3 Object Lock audit ship connector with SigV4 PUT, exclusive object creation, optional retention/legal-hold headers, and receipt upload.
- Opt-in API audit seal-and-ship scheduling policy with full-chain verification, redacted target metadata, and audited success/failure decisions.
- API and MCP request metrics with trace IDs.
- OpenTelemetry-compatible `?format=otlp` metric export and W3C `traceparent` propagation.
- Prometheus and OTLP request/run duration histogram buckets.
- Optional OTLP JSON push configuration for API and MCP Gateway.
- MCP Origin guard.
- SSH host key verification.
- Organization provisioning UI and MCP tools for mapped OIDC memberships.
- Audited worker enqueue, queue dispatch, queue dispatch failure, and automatic retry decisions.
- Web Console bearer-token Session control and audited API logout endpoint.
- Server-side OIDC bearer-token revocation denylist for API/MCP plus expired revocation retention cleanup.
- OIDC bearer-to-HttpOnly browser session exchange, cookie-based API auth, logout session revocation, and expired session retention cleanup.
- Upstream IdP authorization-code login with PKCE, nonce validation, one-time login state consumption, and audited callback/session creation.
- OIDC back-channel logout endpoint that verifies logout tokens, requires the back-channel event claim, and revokes browser sessions by upstream `sid` or subject fallback.
- Expired or consumed OIDC login state retention cleanup through CLI and scheduled API worker.

Next work:

- Register production IdP redirect URLs and add provider-specific private-key JWT or mTLS client authentication if the enterprise IdP requires it.
