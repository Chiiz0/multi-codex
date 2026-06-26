---
name: company-git-sync
description: Use this skill when preparing a reviewed Codex task branch for PR after tests, audit, and approvals have passed.
---

# Company Git Sync Skill

Rules:

- Run only after Gateway says tests, audit, and approvals passed.
- Do not merge to protected branches.
- Do not force push without explicit human approval.
- Prepare PR material and report conflicts.

Required output:

```json
{
  "status": "ready | blocked | failed",
  "branch": "",
  "pr_body_path": "",
  "conflicts": [],
  "needs_human": []
}
```
