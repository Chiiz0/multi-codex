# AGENTS.md

## Project Direction

Build `multi-codex` according to `multi-codex_technical_plan.md`. Keep the implementation auditable, scoped, and easy to run in Docker.

## Development Rules

- Prefer small, testable backend packages under `internal/`.
- Keep documentation split by topic under `docs/`; do not grow one large append-only document.
- Use the fixed development image declared in `.env.example` and `Makefile`.
- Do not add runtime dependency downloads to ordinary dev commands. Build or refresh the dev image explicitly.
- Keep worker security boundaries explicit: Task Envelope, policy validation, scope check, executor isolation, audit log.

## First Milestones

1. Phase 0: project skeleton, Go module, Vite app, Docker Compose, migration baseline.
2. Phase 1: projects, repositories, tasks, runs, events, and basic policy APIs.
3. Phase 2: MCP Gateway tools backed by the same domain validation.
