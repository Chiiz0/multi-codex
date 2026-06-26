# Executors

`multi-codex` now has three executor-related paths.

## Local Lifecycle Executor

Default development mode:

```text
MULTICODEX_EXECUTOR_MODE=mock
```

This mode does not pretend to modify code. It renders the same auditable run directory as the Docker executor and completes deterministically so developers can verify API, MCP, workflow, artifacts, and audit behavior without credentials.

Run directory files:

- `task.json`
- `prompt.md`
- `AGENTS.override.md`
- `worker.log`
- `result.json`
- `diff.patch`

All files are persisted as artifact metadata.

## Docker Executor

Docker mode:

```text
MULTICODEX_EXECUTOR_MODE=docker
```

The Docker executor:

- prepares a run directory
- renders worker prompt and AGENTS override
- attempts repo mirror and workspace preparation
- applies Agent Profile `timeout_seconds` or `MULTICODEX_WORKER_DEFAULT_TIMEOUT`
- mounts `/runs` and `/workspace` into the worker image
- runs a controlled worker command
- invokes `codex exec` from the fixed worker image
- writes `worker.log`, `result.json`, and `diff.patch`
- records artifacts
- records git-diff based scope checks when changed files exist
- marks expired runs as `timed_out`
- removes a timed-out Docker container by deterministic run container name

The fixed worker image remains explicit:

```text
multi-codex/codex-worker:go1.25-node-vite8
```

The provided image pins `@openai/codex@0.142.2` and verifies `codex --version` during image build. Build it explicitly with:

```bash
make worker-image
```

Worker containers run with `--network none` by default. A task can request network only through `TaskEnvelope.network=true`, and policy accepts that request only when the selected Agent Profile has `network_enabled=true`.

Codex runtime credentials are also explicit. An Agent Profile can request environment variable names in `config.worker_secret_env`, but the Docker executor injects them only when:

- the task envelope enables network
- the name is valid
- the name appears in `MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST`
- the configured worker secret provider can resolve the value

For fresh local checkouts, the seeded `demo-service` repository points at a local `demo-service.git` remote. If that specific seed remote is missing, the executor bootstraps a minimal real bare Git repository, writes `workspace_seed_repo_bootstrap`, and records `worker.seed_repository_bootstrap`. Custom repository paths are not bootstrapped; they fail normally so configuration mistakes stay visible.

The default provider is `env`, which reads from the API process environment. The `file` provider reads a flat JSON object from `MULTICODEX_WORKER_SECRET_FILE_PATH` on every lookup, so rotating the file affects the next worker start without storing plaintext in PostgreSQL:

```json
{
  "OPENAI_API_KEY": "..."
}
```

The `vault` provider reads HashiCorp Vault KV v2 over HTTP at `/<mount>/data/<path>`. It supports token or token-file auth plus Vault Enterprise namespace headers:

```bash
MULTICODEX_WORKER_SECRET_PROVIDER=vault
MULTICODEX_WORKER_VAULT_ADDR=https://vault.example
MULTICODEX_WORKER_VAULT_TOKEN_FILE=/run/secrets/vault-token
MULTICODEX_WORKER_VAULT_MOUNT=kv
MULTICODEX_WORKER_VAULT_SECRET_PATH=multi-codex/worker
```

The executor passes only env names to Docker, records `worker_secret_env` run events, emits `worker.secret_env_decision` audit rows with provider/name/reason metadata, and redacts configured secret values from `worker.log`, `result.json`, `diff.patch`, and Docker output events. It does not store secret values in PostgreSQL.

## Node Assignment

When a run starts, the store assigns an active executor node matching the requested executor kind. Docker runs select active Docker nodes. SSH runs require active nodes with verified host key state. The assigned node is persisted as `runs.executor_node_id`, returned by the API, and included in the initial `worker_spawn` event.

Each node can declare `capacity.concurrency`. The scheduler counts active `preparing` and `running` runs on that node and refuses to start a new run when every eligible node is full. Finished, failed, timed-out, or blocked runs release the slot through their terminal status.

Node selection is capacity-normalized:

1. lowest utilization ratio, computed as active runs divided by concurrency
2. most available slots
3. most recent `last_seen_at`
4. oldest node creation time

This avoids treating a one-slot node and a larger worker host as equivalent when both are idle, while still sending the next run to an idle smaller node once a larger node has active work.

Capacity pressure is now persisted instead of being a dead end:

- `StartRun` still performs the immediate capacity check.
- API and MCP start calls enqueue a `queued` run when capacity is full.
- queued runs store `queue_priority`, `retry_attempt`, `max_attempts`, and `queued_reason` in `runs.result`.
- the API queue dispatcher periodically promotes the highest-priority oldest queued run when capacity is available.
- API and MCP expose manual queue dispatch controls for operators.
- dispatch uses the same node selection and policy-aware executor startup path as direct run start.

Capacity responses include a structured `backpressure` payload with the executor kind, retry-after seconds, available slots, and per-node `active_runs`, `concurrency`, `available_slots`, `utilization`, `selection_rank`, and selection/ineligibility reasons. HTTP callers also receive a `Retry-After` header.

Agent Profile config can set:

```json
{
  "queue_priority": 10,
  "retry_max_attempts": 2
}
```

`retry.max_attempts` is also accepted for nested profile config. When a worker fails before exhausting the configured attempt count, the executor records the failure, enqueues the next attempt, and emits `worker.retry_queued` audit rows.

## SSH / agentd Executor

`cmd/worker-agentd` is a small remote-worker daemon for the SSH executor path. It exposes:

- `GET /healthz`
- `POST /v1/runs`
- `GET /v1/runs/{run_id}/result`
- `GET /v1/runs/{run_id}/logs`

The daemon accepts a controlled run payload, writes prompt/log/result files under `MULTICODEX_RUN_ROOT`, and returns a structured result. It is intentionally not a raw shell service.

HTTP agentd deployments can set `MULTICODEX_AGENTD_TOKEN`. When set, `POST /v1/runs` and `GET /v1/runs/{run_id}/result|logs` require `Authorization: Bearer <token>`. API and MCP executor processes use the same environment variable when calling agentd. `GET /healthz` remains unauthenticated for local health checks; SSH forced-command mode relies on SSH key and forced-command restrictions instead of HTTP bearer auth.

Executor nodes now carry SSH safety metadata:

- `host_key_fingerprint`
- `observed_host_key_fingerprint`
- `host_key_verified`
- `agentd_url`
- `forced_command`

The API exposes host-key verification through:

```text
POST /api/v1/executor-nodes/{node_id}/verify-host-key
```

The SSH executor only selects nodes that are `active` and `host_key_verified=true`. It uses HTTP agentd transport when `agentd_url` is set. When `agentd_url` is empty and both `address` and `forced_command` are set, it uses SSH stdin/stdout transport:

```bash
ssh -T -o BatchMode=yes -o StrictHostKeyChecking=yes <node-address> <forced-command>
```

`MULTICODEX_SSH_PRIVATE_KEY_PATH` can select a deployment key, `MULTICODEX_SSH_KNOWN_HOSTS_PATH` can pin the known-hosts file, and `MULTICODEX_SSH_CONNECT_TIMEOUT` controls SSH connection timeout. The node must still be marked host-key verified before the scheduler selects it.

## Forced Command Mode

`worker-agentd` also supports stdin/stdout mode:

```bash
multi-codex-worker-agentd --forced-command
```

This is the intended command for an SSH `authorized_keys` forced-command wrapper. It accepts the same controlled run JSON payload as HTTP `POST /v1/runs` and writes the same prompt/log/result files. It also returns result JSON on stdout so the API can collect `result.json`, `remote-result.json`, and `worker.log` without opening arbitrary remote shell access.

## Remote Collection

When the SSH executor runs, the API:

1. Renders local `task.json`, `prompt.md`, and `AGENTS.override.md`.
2. Verifies an active SSH node has a matching host key fingerprint.
3. Sends the controlled run payload to HTTP agentd or SSH forced-command transport.
4. Fetches remote `worker.log` and `result.json` over HTTP agentd, or collects stdout/log content from forced-command SSH.
5. Stores local artifacts:
   - `worker_log`
   - `result`
   - `remote_result`
   - `diff`
