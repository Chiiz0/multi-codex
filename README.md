# multi-codex

`multi-codex` is an enterprise platform for coordinating scoped Codex workers through a governed MCP Gateway. The first implementation slice focuses on a runnable API, web console shell, PostgreSQL migration baseline, and a fixed Docker development image.

## Local Development

```bash
cp .env.example .env
make dev-image
make dev-up
```

The dev Compose stack uses local PostgreSQL trust auth, so `POSTGRES_PASSWORD` stays empty in `.env.example`. Set a real `POSTGRES_PASSWORD` in your local `.env` before using the production-style Compose file.

Services:

- API: http://localhost:8080/healthz
- MCP Gateway facade: http://localhost:8090/healthz
- Web console: http://localhost:3000
- PostgreSQL: localhost:5432

The development services all use the fixed image tag in `MULTICODEX_DEV_IMAGE`. Build it once with `make dev-image`; normal compile/debug commands reuse it.

## Useful Commands

```bash
make backend-test
make backend-build
make frontend-install
make frontend-build
make compose-config
make migrate-dev
```

## Documentation

Start with [docs/README.md](docs/README.md). The original technical plan is preserved in [multi-codex_technical_plan.md](multi-codex_technical_plan.md), while implementation notes are split into focused folders under `docs/`.
