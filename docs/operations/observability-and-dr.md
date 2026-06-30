# Observability And DR

This page covers production monitoring, alerting, backup/restore drills, audit
handoff, and retention approval.

## Metrics

Scrape:

- API: `GET /metrics?format=prometheus`
- MCP Gateway: `GET /metrics?format=prometheus`

The endpoints expose:

- HTTP request count, error count, and duration histograms
- run count, active run count, and run-duration histograms by role, executor,
  and status
- queue depth by executor
- current terminal worker failures
- audit-derived counters and last-seen timestamps for audit ship, retention,
  and telemetry push health

Optional OTLP push:

```bash
MULTICODEX_TELEMETRY_PUSH_URL=https://collector.example/v1/metrics
MULTICODEX_TELEMETRY_PUSH_INTERVAL=1m
```

Failed telemetry pushes emit `api.telemetry_push_failed` or
`mcp.telemetry_push_failed` audit rows and Prometheus operational metrics.

## Dashboards And Alerts

Import:

- [Grafana dashboard](../../deployments/observability/grafana-dashboard.json)
- [Prometheus alert rules](../../deployments/observability/prometheus-alerts.yaml)

The alert rules cover:

- API/MCP latency and error rate
- queue depth
- worker terminal failures
- audit ship failures and stale successful audit ship
- retention cleanup failures
- telemetry push failures
- PostgreSQL availability through `postgres_exporter`
- disk usage through `node_exporter`

## Backup And Restore Targets

RPO target: 24 hours for the first controlled deployment. Reduce this once the
pilot team has real change volume data.

RTO target: 4 hours for the first controlled deployment, including restore,
migration, service startup, and smoke checks.

Minimum backup set:

- PostgreSQL dump
- artifact root
- run root
- worktree root
- repo cache if local mirrors must survive restore
- audit seal bundles and receipts

Create a backup:

```bash
mcxctl backup -output /var/lib/multi-codex/backups/$(date -u +%Y%m%dT%H%M%SZ)
```

Restore to a disposable environment first:

```bash
mcxctl restore -input /var/lib/multi-codex/backups/<backup-dir>
mcxctl migrate
mcxctl audit-verify
```

Smoke the restored environment before promoting it:

```bash
curl -fsS https://multi-codex-restore.example.com/healthz
curl -fsS https://mcp-restore.example.com/healthz
curl -fsS https://worker-agentd-restore.internal.example.com/healthz
```

Record drill date, backup path, restore duration, verification result, and any
operator fixes in the release evidence folder.

## Audit Export And Ship

Enable one or both handoff paths:

```bash
MULTICODEX_AUDIT_EXPORT_PATH=/var/lib/multi-codex/audit/audit.jsonl
MULTICODEX_AUDIT_SHIP_ENABLED=true
MULTICODEX_AUDIT_SHIP_INTERVAL=24h
MULTICODEX_AUDIT_SEAL_ROOT=/var/lib/multi-codex/audit-seals/scheduled
MULTICODEX_AUDIT_SHIP_TARGET=s3://audit-bucket/multi-codex
```

Scheduled audit ship verifies the hash chain before writing and shipping a
bundle. Failures emit `api.audit_ship_failed` and are covered by Prometheus
alerts.

## Retention Approval

Start production with:

```bash
MULTICODEX_RETENTION_DRY_RUN=true
```

Move to delete mode only after:

- one dry-run report has been reviewed by the service owner
- audit-critical files remain excluded by policy
- a restore drill has succeeded from a backup created after the dry run
- the approval is captured as a change ticket or release record

Then set:

```bash
MULTICODEX_RETENTION_DRY_RUN=false
```

Retention cleanup emits `api.retention_cleanup` or
`api.retention_cleanup_failed` audit rows and exposes failure counters through
operational metrics.
