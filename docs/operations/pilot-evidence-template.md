# Pilot Evidence Template

Create one copy of this template per pilot run. Store the completed copy with
the pilot issue or release evidence bundle.

## Scope

- Pilot date:
- Team:
- Organization ID:
- Project ID:
- Repository:
- Task ID:
- Feature run ID:
- Test run ID:
- Audit run ID:
- Git prepare run ID:
- Git publish run ID:
- Provider PR URL:

## Dry-Run Evidence

- `policy_validate_task` result:
- `repo_scope_check` result and changed files:
- Worker command policy event:
- Worker dependency policy event:
- Worker network policy event:
- Worker resource policy event:
- Worker secret decision event:
- `git_prepare_pr` artifact ID:
- `pr_publish_plan.auto_merge=false` confirmed by:
- `git_publish_pr.status=publish_prepared` confirmed by:
- `git_publish_pr.dry_run=true` confirmed by:
- Audit seal path:
- Audit ship receipt:
- Backup path:

## Live PR Evidence

- `MULTICODEX_GIT_SYNC_LIVE_REVIEWED=true` change approved by:
- Git credential provider:
- `git_publish_pr.status=published` confirmed by:
- `git_publish_pr.dry_run=false` confirmed by:
- `git_publish_pr.credential_resolved=true` confirmed by:
- `git_publish_pr.auto_merge=false` confirmed by:
- Provider PR is open and reviewable by:
- Provider auto-merge is disabled by:

## Recovery Evidence

- Image rollback exercise result:
- Restore drill target:
- `mcxctl migrate` result:
- `mcxctl audit-verify` result:
- Live PR mode disabled after exercise:
- Pilot Git token rotated or revoked:

## Decisions

- Service owner sign-off:
- Security reviewer sign-off:
- Operator sign-off:
- Pilot team usability sign-off:
- Follow-up issue links:
