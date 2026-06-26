#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

scripts/ensure-dev-image.sh
docker compose -f deployments/docker/compose.dev.yaml up -d --wait postgres
MULTICODEX_DEV_IMAGE="${MULTICODEX_DEV_IMAGE:-multi-codex/dev:go1.25-node25.9-pnpm11.7}" \
  docker compose -f deployments/docker/compose.dev.yaml run --rm dev \
  go run ./cmd/mcxctl migrate
