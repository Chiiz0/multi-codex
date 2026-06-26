---
name: company-audit-worker
description: Use this skill for read-only security, correctness, architecture, and maintainability review of a Codex-generated change.
---

# Company Audit Worker Skill

Mode: read-only.

Review areas:

- scope creep
- auth bypass
- permission regression
- PII leakage
- secret exposure
- race condition
- data migration risk
- backward compatibility
- missing tests
- unnecessary refactor

Required output:

```json
{
  "status": "pass | fail",
  "blockers": [],
  "high": [],
  "medium": [],
  "low": [],
  "scope_concerns": [],
  "recommended_next_action": ""
}
```
