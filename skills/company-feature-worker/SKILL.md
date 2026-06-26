---
name: company-feature-worker
description: Use this skill when implementing one scoped product or backend feature in allowed paths only.
---

# Company Feature Worker Skill

Rules:

- Modify only files under `allowed_paths`.
- Never modify `forbidden_paths`.
- Do not push.
- Do not rebase.
- Do not change dependencies unless explicitly allowed.
- Stop if implementation requires a product or architecture decision not present in the task.

Required output:

```json
{
  "status": "done | blocked | failed",
  "changed_files": [],
  "summary": "",
  "tests_run": [],
  "tests_failed": [],
  "risks": [],
  "needs_human": []
}
```
