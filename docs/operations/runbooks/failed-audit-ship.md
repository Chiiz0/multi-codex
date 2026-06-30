# Failed Audit Ship Runbook

## Symptoms

- `MultiCodexAuditShipFailed` or `MultiCodexAuditShipStale` fires.
- Audit logs contain `api.audit_ship_failed`.
- S3 Object Lock, WORM directory, or SIEM collector rejects a bundle.

## Immediate Actions

1. Do not delete local seal output directories.
2. Run `mcxctl audit-verify` against the database.
3. Check ship target credentials, bucket/object-lock policy, endpoint, and disk
   space.

## Recovery

1. If hash-chain verification fails, stop audit ship and preserve the database
   and export file for investigation.
2. If target delivery failed, repair credentials or destination policy and run:
   `mcxctl audit-ship -input <seal-dir> -target <target>`.
3. Confirm a receipt is written and the alert clears.

## Evidence

Record manifest hash, receipt hash, target, error, verification summary, and
the operator who retried the ship.
