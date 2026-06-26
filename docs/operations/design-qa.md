# Design QA Report

Last verified: 2026-06-26.

## Scope

- Viewport: default in-app browser desktop viewport, `1280px` CSS width.
- State: local web console with API-offline empty state, then local in-memory API with seeded owner session.
- Evidence: screenshots were captured under the local `.tmp/` directory during implementation and are intentionally not committed.

## Findings

- No P0/P1/P2 findings.

## Open Questions

- The reference mock showed populated task, run, audit, and artifact data; the first implementation pass also verified an API-offline empty state. A second QA pass with seeded backend data should tune density, row rhythm, and evidence panels against realistic records.

## Implementation Checklist

- Rebuilt the dashboard as a cockpit with work queue, queue health, lifecycle gates, live run evidence, MCP tool calls, artifacts, and audit trail.
- Preserved existing API calls and mutations while adding repository selection and safer local offline behavior.
- Added polling safeguards so failed API requests stop repeated refresh loops in frontend-only development.
- Verified production build with `pnpm build`.
- Verified local screenshot has no horizontal overflow at `1280px` CSS width.
- Verified basic interactions: task filter button and Tasks navigation.

## Integration QA Addendum: Auth, Permissions, I18n

- `pnpm build` passed.
- `go test ./...` passed.
- `docker compose -f deployments/docker/compose.dev.yaml config >/dev/null` passed.
- Browser smoke at `1280px` passed: no horizontal overflow, no console errors, Chinese language persisted to `localStorage`, and Tasks navigation changed hash to `#tasks`.
- Verified frontend can fetch `/api/v1/auth/me` through the Vite proxy and render the local owner role plus all-permissions badge.

## Follow-Up Polish

- [P3] Source mock includes navigation/action icons; implementation keeps text-only controls because no icon library is currently installed in the Vite app. Add a project-approved icon package or design-system icon set before matching this detail.
- [P3] Capture a seeded-task state later to tune the operational dashboard against real data.
