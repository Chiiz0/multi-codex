# Stuck Queue Runbook

## Symptoms

- `MultiCodexQueueDepthHigh` fires.
- `/api/v1/queue` shows queued runs and no available executor slots.
- Workers stay in `preparing` or `running` longer than profile timeout.

## Immediate Actions

1. Check executor node status and capacity.
2. Confirm worker-agentd and Docker/SSH worker hosts are healthy.
3. Avoid manual dispatch in OIDC multi-organization mode; it is intentionally
   disabled until org-scoped dispatch is supported.

## Recovery

1. Fix unhealthy executor nodes or add capacity.
2. For dead workers, collect logs and let timeout cleanup release capacity.
3. If the queue is blocked by policy, resolve approvals, scope checks, tests, or
   audit gates rather than forcing dispatch.

## Evidence

Record queue snapshot, backpressure payload, affected run IDs, executor node
IDs, and audit rows for enqueue, dispatch, blocked dispatch, or retry.
