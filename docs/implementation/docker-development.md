# Docker Development

Development uses one fixed toolchain image:

```text
multi-codex/dev:go1.25-node25.9-pnpm11.7
```

This image contains Go, Node, pnpm, Git, PostgreSQL client tools, and common shell utilities. Build it explicitly once:

```bash
make dev-image
```

Normal development commands reuse that image:

```bash
make dev-up
make dev-shell
make frontend-build
```

The Codex worker image is also fixed and built explicitly:

```bash
make worker-image
```

That target builds `multi-codex/codex-worker:go1.25-node-vite8` with pinned `@openai/codex@0.142.2`. It is separate from the dev image so normal backend/frontend loops do not download runtime worker dependencies.

Docker worker credentials are opt-in. Add env names to `MULTICODEX_WORKER_SECRET_ENV_ALLOWLIST`, configure an `env`, `file`, or `vault` worker secret provider, and create an Agent Profile whose `config.worker_secret_env` requests only those names. The task envelope must also set `network=true` and use a network-enabled Agent Profile.

## Why This Shape

- The dev image changes only when the toolchain version changes.
- Source code is mounted into the container, so backend and frontend compile/debug loops do not rebuild images.
- Go and pnpm caches are stored in Docker volumes.
- Production images stay separate from the dev image.
- The Codex worker runtime is refreshed only through `make worker-image`.
- Worker credentials are injected by name only through the executor allowlist.

## Compose Files

- `deployments/docker/compose.dev.yaml`: local development with the fixed image.
- `deployments/docker/compose.yaml`: first production-style deployment shape.

The dev Compose file uses local PostgreSQL trust auth and passwordless connection URLs. The production-style Compose file requires `POSTGRES_PASSWORD` from local environment or `.env`; `.env.example` intentionally leaves it blank.

PostgreSQL 18 must mount its volume at `/var/lib/postgresql`, not `/var/lib/postgresql/data`.

## Refreshing the Image

Update the tag in these places together:

- `.env.example`
- `Makefile`
- `deployments/docker/compose.dev.yaml`
- `scripts/ensure-dev-image.sh`

Then run:

```bash
make dev-image
```

Update `CODEX_CLI_VERSION` in `Makefile` only when intentionally refreshing the worker runtime, then run:

```bash
make worker-image
```
