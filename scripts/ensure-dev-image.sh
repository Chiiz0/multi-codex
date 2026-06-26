#!/usr/bin/env bash
set -euo pipefail

IMAGE_TAG="${DEV_IMAGE_TAG:-${MULTICODEX_DEV_IMAGE:-multi-codex/dev:go1.25-node25.9-pnpm11.7}}"

if docker image inspect "${IMAGE_TAG}" >/dev/null 2>&1; then
  echo "Using existing development image: ${IMAGE_TAG}"
  exit 0
fi

echo "Development image ${IMAGE_TAG} not found. Building it once..."
docker build -f deployments/docker/Dockerfile.dev -t "${IMAGE_TAG}" .
