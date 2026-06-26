#!/usr/bin/env bash
set -euo pipefail

if [[ -f /runs/prompt.md ]]; then
  echo "multi-codex worker: received /runs/prompt.md"
else
  echo "multi-codex worker: no prompt mounted yet"
fi

if [[ $# -gt 0 ]]; then
  exec "$@"
fi

exec bash
