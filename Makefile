SHELL := /bin/bash

DEV_IMAGE_TAG ?= multi-codex/dev:go1.25-node25.9-pnpm11.7
WORKER_IMAGE_TAG ?= multi-codex/codex-worker:go1.25-node-vite8
CODEX_CLI_VERSION ?= 0.142.2
COMPOSE ?= docker compose
COMPOSE_DEV := $(COMPOSE) -f deployments/docker/compose.dev.yaml
COMPOSE_PROD := $(COMPOSE) -f deployments/docker/compose.yaml

.PHONY: dev-image worker-image ensure-dev-image dev-shell dev-up dev-down backend-test backend-build frontend-install frontend-build compose-config migrate-dev

dev-image:
	docker build -f deployments/docker/Dockerfile.dev -t $(DEV_IMAGE_TAG) .

worker-image:
	docker build -f deployments/docker/Dockerfile.worker --build-arg CODEX_CLI_VERSION=$(CODEX_CLI_VERSION) -t $(WORKER_IMAGE_TAG) .

ensure-dev-image:
	DEV_IMAGE_TAG=$(DEV_IMAGE_TAG) scripts/ensure-dev-image.sh

dev-shell: ensure-dev-image
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) run --rm dev bash

dev-up: ensure-dev-image
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) up postgres api-dev mcp-gateway-dev worker-agentd-dev web-dev

dev-down:
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) down

backend-test:
	GOCACHE=$${GOCACHE:-$(PWD)/.cache/go-build} go test ./...

backend-build:
	GOCACHE=$${GOCACHE:-$(PWD)/.cache/go-build} go build ./cmd/api ./cmd/mcp-gateway ./cmd/worker-agentd ./cmd/mcxctl

frontend-install: ensure-dev-image
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) run --rm dev pnpm --dir apps/web install

frontend-build: ensure-dev-image
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) run --rm dev bash -lc 'pnpm --dir apps/web install --frozen-lockfile && pnpm --dir apps/web build'

compose-config:
	MULTICODEX_DEV_IMAGE=$(DEV_IMAGE_TAG) $(COMPOSE_DEV) config >/dev/null
	POSTGRES_PASSWORD="$${POSTGRES_PASSWORD:-$$(openssl rand -hex 16)}" $(COMPOSE_PROD) config >/dev/null

migrate-dev:
	scripts/migrate-dev.sh
