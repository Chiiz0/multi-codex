# Runaway Worker Runbook

## Symptoms

- Worker timeout, terminal failure, CPU, memory, pids, or disk alerts fire.
- Docker containers or SSH workers continue after the run should have stopped.
- Run events show repeated `executor_timeout` or `worker_retry_queued`.

## Immediate Actions

1. Isolate the worker host from outbound network if credential risk is possible.
2. Stop accepting new worker runs on the affected executor node.
3. Preserve `worker.log`, `result.json`, `diff.patch`, run events, and audit
   rows.

## Recovery

1. Terminate the runaway container or remote process.
2. Verify Docker resource limits, timeout policy, network mode, and secret
   injection decisions for the run.
3. Rotate any credentials that were injected into the worker.
4. Re-enable capacity only after a clean test run.

## Evidence

Record run ID, task ID, executor node, applied resource policy, timeout,
network decision, injected env names, and credential rotation ticket.
