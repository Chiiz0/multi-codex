# Architecture Overview

`multi-codex` coordinates multiple role-specific Codex workers through a governed MCP Gateway.

## Control Flow

```text
Human Lead
  -> Web Console / Main Codex
  -> MCP Gateway
  -> API / Scheduler
  -> Docker Executor or SSH Executor
  -> Feature/Test/Audit/Git Sync Codex Worker
```

## MVP Boundaries

- Main Codex calls controlled MCP tools only.
- Worker Codex instances run in isolated worktrees.
- Every task starts from a structured Task Envelope.
- Scope checks evaluate the real changed file list after code-producing runs.
- Test and audit runs must pass before Git Sync prepares PR material.
- Human approval remains the merge boundary.

## Current Implementation Slice

The current codebase implements:

- Go API with PostgreSQL and in-memory storage.
- Resource APIs for organizations, projects, repositories, tasks, runs, events, artifacts, approvals, Skills, Agent Profiles, executor nodes, browser auth sessions, tool calls, and audit logs.
- MCP Gateway with Streamable HTTP-style JSON-RPC endpoint, compatibility tool endpoints, TTL-bound persisted sessions, durable SSE replay, PostgreSQL LISTEN/NOTIFY plus polling fallback for cross-replica active-stream fanout, and audited resume/fanout handling.
- Workflow state machine gates for feature, scope, test, audit, approval, and git sync.
- Docker executor path with run rendering, workspace preparation, artifact collection, pinned Codex CLI worker image, and policy-gated network mode.
- Capacity-aware executor node assignment for Docker and verified SSH nodes.
- Persisted worker queue with priority ordering, retry enqueue policy, and API/MCP dispatch controls.
- Git Sync PR body preparation plus gated dry-run or live GitHub/GitLab PR creation with env/file/Vault provider credential resolution.
- worker-agentd service for SSH executor HTTP and forced-command transports.
- Vite/React Web Console for dashboard, tasks, runs, approvals, nodes, organizations, Skills/Profiles, audit, and MCP tool calls.
- Enterprise operations CLI for migrations, retention cleanup, backup/restore, audit hash-chain verification, and audit seal/ship handoff to file, HTTP/S, or S3 Object Lock targets.
- OIDC enterprise auth with bearer validation, browser HttpOnly sessions, authorization-code login with PKCE, token/session revocation, and back-channel logout.
- API/MCP metrics with Prometheus and OTLP request/run histograms plus optional OTLP push.
- Fixed Docker development image and compose workflow.

Higher-throughput external MCP fanout buses such as Redis Streams or NATS remain optional enterprise scaling enhancements beyond the PostgreSQL-backed fanout path.

## Related Architecture Docs

- [Task Envelope](task-envelope.md)
- [MCP Gateway](mcp-gateway.md)
- [Executors](executors.md)
