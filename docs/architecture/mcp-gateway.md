# MCP Gateway

The MCP Gateway is the policy enforcement boundary between Main Codex and the platform.

## Transport

The gateway exposes:

- `POST /mcp` for JSON-RPC requests.
- `GET /mcp` for a Server-Sent Events stream.
- `GET /tools` and `POST /tools/call` as compatibility endpoints for simple local checks.

The JSON-RPC endpoint implements the current Streamable HTTP shape used by MCP:

- `initialize`
- `ping`
- `tools/list`
- `tools/call`

The implementation follows the 2025-06-18 Streamable HTTP direction: one MCP endpoint supports POST and optional GET/SSE. It returns JSON responses for ordinary requests and assigns an `MCP-Session-Id` header. A valid client-provided session id is echoed; otherwise the gateway generates an `mcp_session_*` id.

MCP sessions are persisted through the shared store with a configurable TTL:

```bash
MULTICODEX_MCP_SESSION_TTL=8h
```

Responses include `MCP-Session-Expires-At`. Active requests extend the session deadline. Expired sessions return `404` and emit `mcp.session_expired`. SSE streams persist emitted ready and heartbeat messages to `mcp_session_events` with monotonic per-session `id:` fields. A client can reconnect with `Last-Event-ID`; the gateway replays persisted events after that id, emits `mcp.session_resume`, records `replayed_events`, and then appends a new ready event. If a client reconnects with an event id higher than the persisted high-water mark, the session floor is advanced before the next event is appended so ids never move backward across gateway restarts.

Conformance checks currently enforce:

- `POST /mcp` must include `Accept: application/json, text/event-stream`.
- `GET /mcp` must include `Accept: text/event-stream`.
- JSON-RPC notifications such as `notifications/initialized` return `202 Accepted` with no body.
- MCP responses include `MCP-Protocol-Version` and `MCP-Session-Id`.
- `initialize`, `notifications/*`, SSE stream-open, resume, and expired lifecycle events emit `mcp.session_*` audit rows.
- `Last-Event-ID` resume replays persisted `mcp_session_events` before appending the next ready event.
- The metrics middleware preserves `http.Flusher` so SSE still works through instrumentation.

## Tool Boundary

Supported tools:

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

The gateway intentionally does not expose raw shell, raw Docker, raw SSH, raw Git push, secret read, or arbitrary file-write tools.

## Persistence and Audit

Every MCP session is persisted to `mcp_sessions`, every replayable SSE message is persisted to `mcp_session_events`, and every tool call is persisted to `tool_calls`. PostgreSQL stores publish a best-effort `pg_notify` message after a session event commits; active SSE streams subscribe with `LISTEN` and then read the durable event table before sending data to the client. Streams also poll the persisted session-event table on `MULTICODEX_MCP_LIVE_FANOUT_INTERVAL` as a fallback, so an event appended by one gateway replica can be delivered to a subscriber connected to another replica without waiting for reconnect replay.

Every tool call also emits an `audit_logs` record with:

- actor: `codex/main`
- action: `mcp_tool.<tool_name>`
- resource type and id
- status

Workflow-sensitive tools run through the same state gates as the Web API:

- scope violation blocks later gates
- test failure blocks audit and git sync
- audit blocker blocks git sync
- rejected approval blocks git sync

Queue-sensitive tools use the same persisted scheduler state as the API:

- capacity-full worker starts enqueue a durable `queued` run instead of dropping the request
- `queue_status` returns queued runs plus docker/ssh backpressure snapshots
- `queue_dispatch` promotes one queued run through the same node assignment and executor startup path as the API queue worker
- queue enqueue and dispatch decisions emit dedicated `mcp.worker_enqueue`, `mcp.queue_dispatch`, and blocked/failed audit rows in addition to `mcp_tool.*` records

Session-sensitive stream decisions emit `mcp.session_initialize`, `mcp.session_notification`, `mcp.session_stream_open`, `mcp.session_resume`, `mcp.session_notify_fanout`, `mcp.session_fanout`, and `mcp.session_expired` audit rows. Notification subscription failures emit `mcp.session_notify_subscribe_failed` and continue with polling fallback.

## Security Defaults

The MCP Gateway rejects non-local `Origin` headers by default to reduce local DNS rebinding exposure during development. When `MULTICODEX_AUTH_MODE=oidc`, `/mcp`, `/tools`, and `/tools/call` require an RS256 Bearer JWT validated against the configured OIDC issuer/audience/JWKS settings. Health and metrics remain available for operations checks.
