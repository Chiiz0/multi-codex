# Compromised Worker Credential Runbook

## Symptoms

- Secret appears in external logs, worker output, or provider alerts.
- Unexpected repository, API, or model-provider activity from a worker token.
- Worker host or task is suspected compromised.

## Immediate Actions

1. Disable affected Agent Profiles or remove the secret name from
   `worker_secret_env`.
2. Revoke or rotate the provider credential.
3. Isolate affected worker hosts and stop new worker runs.

## Recovery

1. Replace file/Vault/env secret value and confirm resolver picks up the rotated
   value on the next run.
2. Review run events for `worker_secret_env`, `worker.network_policy`, and
   `worker.resource_policy`.
3. Run a clean scoped worker task with the rotated credential.

## Evidence

Record credential name, provider, affected run IDs, redaction findings,
rotation time, and post-rotation validation result. Do not record secret values.
