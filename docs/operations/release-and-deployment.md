# Release And Deployment

This page is the operator path for building, scanning, deploying, and rolling
back a production release. Run commands from the repository root.

## CI Gates

`.github/workflows/ci.yml` verifies pull requests and `main` pushes with the
fixed dev image:

- backend tests: `go test ./...`
- backend builds: API, MCP Gateway, worker-agentd, and `mcxctl`
- frontend production build
- dev and production Compose config validation
- migration check against PostgreSQL 18
- API, Web, and worker image builds
- SBOM and Trivy SARIF scan artifacts for each release image

Tag pushes matching `v*` publish immutable GHCR tags:

- `ghcr.io/<owner>/<repo>/api:<tag>`
- `ghcr.io/<owner>/<repo>/api:sha-<commit>`
- `ghcr.io/<owner>/<repo>/web:<tag>`
- `ghcr.io/<owner>/<repo>/web:sha-<commit>`
- `ghcr.io/<owner>/<repo>/codex-worker:<tag>`
- `ghcr.io/<owner>/<repo>/codex-worker:sha-<commit>`

## Manual Release Build

Build the same artifacts locally when cutting or verifying a release:

```bash
export RELEASE_TAG=v0.1.0
docker build -f deployments/docker/Dockerfile.api -t multi-codex/api:${RELEASE_TAG} .
docker build -f deployments/docker/Dockerfile.web -t multi-codex/web:${RELEASE_TAG} .
docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=0.142.2 -t multi-codex/codex-worker:${RELEASE_TAG} .
```

Generate SBOMs and vulnerability scans with release-workstation tooling:

```bash
syft multi-codex/api:${RELEASE_TAG} -o spdx-json=sbom-api.spdx.json
syft multi-codex/web:${RELEASE_TAG} -o spdx-json=sbom-web.spdx.json
syft multi-codex/codex-worker:${RELEASE_TAG} -o spdx-json=sbom-worker.spdx.json
trivy image --format sarif --output trivy-api.sarif multi-codex/api:${RELEASE_TAG}
trivy image --format sarif --output trivy-web.sarif multi-codex/web:${RELEASE_TAG}
trivy image --format sarif --output trivy-worker.sarif multi-codex/codex-worker:${RELEASE_TAG}
```

Store SBOMs, scan outputs, image digests, migration version, and the Git commit
SHA with the release record.

## Compose Deployment

Start from `.env.production.example`, fill secrets from the deployment secret
manager, and point image variables at immutable tags or digests:

```bash
MULTICODEX_API_IMAGE=ghcr.io/acme/multi-codex/api:v0.1.0
MULTICODEX_WEB_IMAGE=ghcr.io/acme/multi-codex/web:v0.1.0
MULTICODEX_WORKER_IMAGE=ghcr.io/acme/multi-codex/codex-worker:v0.1.0
```

Validate and migrate before starting serving processes:

```bash
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:?}" docker compose -f deployments/docker/compose.yaml config >/dev/null
docker compose -f deployments/docker/compose.yaml --profile migrate run --rm migrate
docker compose -f deployments/docker/compose.yaml up -d postgres api mcp-gateway worker-agentd web
```

Smoke checks:

```bash
curl -fsS https://multi-codex.example.com/healthz
curl -fsS https://mcp.multi-codex.example.com/healthz
curl -fsS https://worker-agentd.internal.example.com/healthz
```

## Migration Pattern

The production image includes `mcxctl`. The `migrate` Compose profile runs:

```bash
mcxctl migrate
```

Run migrations once per deploy before application rollout. Keep database
backups and the previous image tags available until smoke checks and audit ship
checks pass.

## TLS And Reverse Proxy

Terminate TLS in a managed load balancer, ingress controller, or reverse proxy.
Route public traffic as follows:

- Web Console: `https://multi-codex.example.com/` to `web:80`
- API: `https://multi-codex.example.com/api/` to `api:8080`
- MCP Gateway: `https://mcp.multi-codex.example.com/` to `mcp-gateway:8090`
- worker-agentd: keep internal only; expose through private network policy when
  HTTP agentd is used.

Forward `X-Forwarded-Proto`, `X-Forwarded-Host`, and request IDs. Set
`MULTICODEX_AUTH_COOKIE_SECURE=true`, exact HTTPS CORS origins, and the final
OIDC redirect URL before production mode starts.

## Rollback

Application rollback:

1. Set `MULTICODEX_API_IMAGE`, `MULTICODEX_WEB_IMAGE`, and
   `MULTICODEX_WORKER_IMAGE` back to the previous immutable tags.
2. Run `docker compose -f deployments/docker/compose.yaml up -d api mcp-gateway worker-agentd web`.
3. Re-run health checks and verify `/metrics` error rates and queue depth.

Database rollback:

1. Prefer forward-fix migrations. The migration runner is idempotent and does
   not include destructive down migrations.
2. If a schema/data rollback is required, stop application services, restore
   the last verified backup to a disposable environment, verify with
   `mcxctl audit-verify`, then promote the restored database.
3. Preserve audit seal bundles and release evidence even when the application
   images roll back.
