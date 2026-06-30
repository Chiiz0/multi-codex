# Product Pilot Runbook

Use this runbook for the first controlled production pilot on one low-risk
internal repository. The pilot goal is to prove a governed workflow can create a
reviewable provider PR while keeping auto-merge disabled and every security
boundary visible in audit evidence.

## Entry Criteria

- The deployment is running production mode with OIDC, secure cookies, explicit
  CORS origins, retention, audit ship, and worker limits enabled.
- `MULTICODEX_GIT_SYNC_MODE=dry-run` is the starting state.
- The pilot repository has a normal human-owned branch protection and review
  policy.
- The Git provider token can create branches and PRs for the pilot repository
  only. It must not have merge bypass privileges.
- A named service owner, security reviewer, operator, and pilot engineer are
  assigned.

## Provisioning

1. Create or select the pilot organization in multi-codex.
2. Map the pilot IdP group to that organization with
   `MULTICODEX_OIDC_GROUP_ORG_MAP`.
3. Map the team roles:

```bash
MULTICODEX_OIDC_GROUP_ROLE_MAP=pilot-engineers=tech_lead;pilot-reviewers=reviewer;pilot-operators=operator;pilot-auditors=auditor
```

4. Create one project for the pilot and one repository record for the low-risk
   repository.
5. Register the required Skills: `company-feature-worker`,
   `company-test-worker`, `company-audit-worker`, and `company-git-sync`.
6. Create Agent Profiles for feature, test, audit, and git-sync roles. Keep
   worker network disabled unless the task and repository need it.
7. Register one executor node with known resource limits. For SSH workers,
   verify host keys before any task is queued.
8. Confirm Git credentials are configured but dry-run remains active:

```bash
MULTICODEX_GIT_SYNC_MODE=dry-run
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=false
MULTICODEX_GIT_CREDENTIAL_PROVIDER=file
MULTICODEX_GIT_CREDENTIAL_FILE_PATH=/run/secrets/multi-codex-git.json
```

## Pilot Task Envelope

Start with a narrow change and explicit stop conditions:

```json
{
  "title": "Pilot: small documentation or test-only change",
  "role": "feature",
  "allowed_paths": ["docs/**", "tests/**"],
  "forbidden_paths": [".env*", "secrets/**", ".github/**", "infra/**", "terraform/**"],
  "allowed_commands": ["go test ./...", "pnpm --dir apps/web build"],
  "network": false,
  "policy": {
    "allow_push": false,
    "allow_dependency_change": false,
    "allow_infra_change": false,
    "require_audit": true,
    "require_tests": true,
    "require_human_before_pr": true
  },
  "stop_conditions": [
    "scope check blocks the change",
    "test or audit run blocks the change",
    "provider PR requires unapproved credentials or merge permissions"
  ]
}
```

## Dry-Run Workflow

Run the full governed flow before enabling live provider calls:

1. Create and validate the task.
2. Start the feature worker.
3. Confirm `repo_scope_check` passed and changed files match the envelope.
4. Run the test worker and audit worker.
5. Approve `pr_prepare`.
6. Run `git_prepare_pr` and review the generated `pr_body` artifact plus
   `pr_publish_plan`.
7. Approve `pr_publish`.
8. Run `git_publish_pr` while still in dry-run mode.
9. Export evidence:

```bash
mcxctl audit-verify
mcxctl audit-seal -output /var/lib/multi-codex/audit-seals/pilot-dry-run
mcxctl audit-ship -input /var/lib/multi-codex/audit-seals/pilot-dry-run -target "$MULTICODEX_AUDIT_SHIP_TARGET"
mcxctl backup -output /var/lib/multi-codex/backups/pilot-dry-run
```

Dry-run evidence must show:

- Task Envelope validation passed.
- Scope, dependency, command, network, resource, timeout, and secret decisions
  are present in run events or audit rows.
- `git_prepare_pr` produced `auto_merge=false`, `allow_push=false`, and
  `required_approval=pr_publish`.
- `git_publish_pr` produced `status=publish_prepared`, `dry_run=true`, and
  `auto_merge=false`.
- The audit seal shipped successfully.
- A backup was created after the dry-run workflow.

`publish_prepared` is a reviewable dry-run evidence point, not the terminal
workflow state. After the dry-run evidence is approved and live mode is enabled,
the same governed task remains eligible for one more `git_publish_pr` action.
The workflow reaches completed only after a provider call records
`status=published`.

## Live PR Creation

Enable live mode only after the service owner, security reviewer, and operator
record approval in the pilot evidence:

```bash
MULTICODEX_GIT_SYNC_MODE=live
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true
```

Production validation refuses API and MCP startup with live Git Sync unless the
review flag is true and the configured credential provider can resolve provider
tokens. Supported providers are `env`, `file`, and `vault`.

Rerun only the PR publish step for the approved task. The resulting run must
record:

- `status=published`
- `dry_run=false`
- `credential_provider`
- `credential_resolved=true`
- `auto_merge=false`
- `pr_url`

Verify the provider PR is reviewable by humans and not merged automatically.
Do not enable provider auto-merge, merge queues, or bot-owned merge bypass for
the pilot token.

## Rollback And Recovery Exercise

Exercise all three paths before pilot sign-off:

1. Roll back the deployment image tag using the release rollback procedure.
2. Restore the dry-run backup into a disposable environment and run:

```bash
mcxctl migrate
mcxctl audit-verify
```

3. Disable live PR creation:

```bash
MULTICODEX_GIT_SYNC_MODE=dry-run
MULTICODEX_GIT_SYNC_LIVE_REVIEWED=false
```

4. Revoke or rotate the pilot Git provider token.

## Sign-Off

Record sign-off in a focused issue or pilot evidence document with:

- team, repository, task IDs, run IDs, and PR URL
- dry-run audit seal destination and receipt
- live PR creation audit evidence
- backup and restore drill timestamp
- security reviewer decision
- operator decision
- pilot team usability decision
- follow-up issues for product, security, or operations gaps

Broader rollout is blocked until the sign-off states that security,
operations, and product usability are acceptable for the next team.

## Evidence Verification

After the dry-run, live PR creation, restore drill, audit ship, and sign-off
are recorded, run the strict verifier from the production admin environment:

```bash
mcxctl pilot-verify \
  -task-id <pilot-task-id> \
  -audit-ship-receipt /var/lib/multi-codex/audit-ship/<bundle>/receipt.json \
  -backup-manifest /var/lib/multi-codex/backups/pilot-dry-run/manifest.json \
  -restore-evidence /var/lib/multi-codex/restore-drills/pilot-restore.json \
  -signoff /var/lib/multi-codex/pilot-evidence/<pilot-task-id>-signoff.md
```

The command exits non-zero unless the database evidence shows:

- Task Envelope resource validation passed.
- Feature, test, and audit runs succeeded.
- Scope check passed.
- `pr_prepare` and `pr_publish` approvals are approved.
- `git_prepare_pr` produced a publish plan with `auto_merge=false` and
  `required_approval=pr_publish`.
- A dry-run `git_publish_pr` run produced `status=publish_prepared`,
  `dry_run=true`, and `auto_merge=false`.
- A live `git_publish_pr` run produced `status=published`, `dry_run=false`,
  `credential_resolved=true`, `auto_merge=false`, and `pr_url`.
- Matching audit rows exist for task creation, PR preparation, dry-run publish,
  and live publish.
- The audit ship receipt, backup manifest, restore evidence, and sign-off files
  exist and parse.

For preflight checks before external evidence files exist, pass
`-strict=false`. Do not use non-strict output for broader rollout sign-off.
