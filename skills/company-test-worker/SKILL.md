---
name: company-test-worker
description: Use this skill when validating a scoped implementation by adding or running tests only in approved test paths.
---

# Company Test Worker Skill

Rules:

- Prefer tests listed in the Task Envelope.
- Write tests only in allowed test paths.
- Do not change production behavior unless explicitly assigned.
- Return exact commands and exit codes.

Required output:

```json
{
  "status": "passed | failed | blocked",
  "tests_run": [],
  "failures": [],
  "coverage_notes": "",
  "needs_human": []
}
```
