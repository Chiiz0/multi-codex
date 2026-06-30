# Production Configuration

Production mode is explicit and fail-closed. Set either:

```bash
MULTICODEX_ENV=production
```

or:

```bash
MULTICODEX_PRODUCTION=true
```

When production mode is enabled, API, MCP Gateway, and worker-agentd validate
configuration before opening stores or serving traffic.

## Required API Configuration

The API refuses to start unless all of these are true:

- `MULTICODEX_AUTH_MODE=oidc`
- `MULTICODEX_OIDC_ISSUER`, `MULTICODEX_OIDC_AUDIENCE`, and
  `MULTICODEX_OIDC_JWKS_URL` are set.
- `MULTICODEX_OIDC_CLIENT_ID` and `MULTICODEX_OIDC_REDIRECT_URL` are set for
  browser login.
- `MULTICODEX_AUTH_COOKIE_SECURE=true`
- `MULTICODEX_DATABASE_URL` includes a non-empty database password.
- `MULTICODEX_CORS_ALLOWED_ORIGINS` contains explicit `https://` origins and no
  wildcard.
- `MULTICODEX_AGENTD_TOKEN` is set when API is configured to call
  worker-agentd.
- Worker execution controls are set and safe: positive timeout, CPU, memory,
  pids limit, read-only root filesystem, tmpfs size, `no-new-privileges`,
  `cap-drop=ALL`, and a non-empty command denylist.
- `MULTICODEX_EXECUTOR_MODE=docker` is rejected unless
  `MULTICODEX_WORKER_DOCKER_SOCKET_ENABLED=true` and
  `MULTICODEX_WORKER_DOCKER_SOCKET_BOUNDARY=isolated-worker-host`.
- `MULTICODEX_GIT_SYNC_MODE` is either `dry-run` or `live`. Production live
  mode is rejected unless `MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true` and the Git
  credential provider is configured.
- Retention is explicitly enabled with positive interval and max age.
- Scheduled audit ship is explicitly enabled with seal root and ship target.

Local development remains unchanged because `.env.example` sets
`MULTICODEX_ENV=development` and keeps local auth and permissive CORS available
for Docker development only.

## Required MCP Gateway Configuration

The MCP Gateway refuses production mode unless:

- `MULTICODEX_AUTH_MODE=oidc`
- `MULTICODEX_OIDC_ISSUER`, `MULTICODEX_OIDC_AUDIENCE`, and
  `MULTICODEX_OIDC_JWKS_URL` are set.
- `MULTICODEX_DATABASE_URL` includes a non-empty database password.
- `MULTICODEX_AGENTD_TOKEN` is set when the gateway is configured to call
  worker-agentd.
- Worker execution controls match the API requirements because MCP tools can
  also start workers.
- Git Sync live-mode checks match the API requirements because MCP tools can
  also create provider PRs.

MCP Streamable HTTP keeps its Origin guard. Browser-facing CORS is enforced at
the API layer.

## Required worker-agentd Configuration

worker-agentd refuses production mode when it listens on a non-loopback address
without:

```bash
MULTICODEX_AGENTD_TOKEN=...
```

`GET /healthz` remains unauthenticated for container health checks. Run creation
and log/result retrieval require the Bearer token.

## CORS Policy

In development, the API can still echo local request origins for ergonomic Vite
usage. In production, or whenever `MULTICODEX_CORS_ALLOWED_ORIGINS` is set, the
API rejects requests with unlisted `Origin` headers.

Use a comma, semicolon, or whitespace separated list:

```bash
MULTICODEX_CORS_ALLOWED_ORIGINS=https://multi-codex.example.com
```

Do not include paths, wildcards, or insecure `http://` origins in production.

## Worker Controls

Production defaults are intentionally constrained:

```bash
MULTICODEX_WORKER_CPUS=1
MULTICODEX_WORKER_MEMORY=2g
MULTICODEX_WORKER_PIDS_LIMIT=256
MULTICODEX_WORKER_READ_ONLY_ROOTFS=true
MULTICODEX_WORKER_TMPFS_SIZE=256m
MULTICODEX_WORKER_NO_NEW_PRIVILEGES=true
MULTICODEX_WORKER_CAP_DROP=ALL
```

Leave Docker socket execution disabled unless the API container is deployed on a
dedicated isolated worker host. The default Compose file does not mount the host
Docker socket.

## Git Sync Live Mode

Dry-run is the production starting point:

```bash
MULTICODEX_GIT_SYNC_MODE=dry-run
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=false
```

Live provider PR creation is opt-in after a dry-run pilot has been reviewed:

```bash
MULTICODEX_GIT_SYNC_MODE=live
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true
```

When `MULTICODEX_GIT_CREDENTIAL_PROVIDER=file`, set
`MULTICODEX_GIT_CREDENTIAL_FILE_PATH`. When the provider is `vault`, set the
Vault address, token or token file, and secret path. When the provider is `env`,
the process must receive at least one provider token such as `GITHUB_TOKEN` or
`GITLAB_TOKEN`.

The workflow still requires the `pr_publish` approval gate, and the PR publish
result records `auto_merge=false`.

## Template

Start from [.env.production.example](../../.env.production.example). Leave
secret values blank in Git and inject them from the deployment secret manager.
