# Database Outage Runbook

## Symptoms

- API/MCP error rate alert fires.
- `pg_up == 0` or database latency spikes.
- Migrations, queue dispatch, auth sessions, or audit writes fail.

## Immediate Actions

1. Stop new deploys and pause manual queue dispatch.
2. Check PostgreSQL health, disk, connections, and recent migrations.
3. Preserve database logs and current image tags.

## Recovery

1. Restore database service or fail over through the managed database process.
2. If restore is required, restore into a disposable environment first, run
   `mcxctl migrate`, then `mcxctl audit-verify`.
3. Restart API, MCP Gateway, and worker-agentd after database health is stable.

## Evidence

Record outage window, last successful backup, restore or failover command,
`audit-verify` result, and post-recovery health checks.
