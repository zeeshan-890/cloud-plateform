# Development

## Prerequisites

Go 1.22+, Node 22 (CI uses 22; the dashboard Dockerfile builds with `node:22-alpine`), Docker with Compose v2, git. Bash scripts need Git Bash/WSL on Windows (PowerShell
equivalents exist for install, jp-up, size-host and test-all).

## Repository

* `go.work` lists 24 modules: `apps/cli`, `packages/events`, `packages/go-common` and the 21 services under `services/`
  (`services/event` is only a placeholder directory with migrations and is not a module). Services depend on `go-common` via
  `replace ../../packages/go-common`, which is also what makes `GOWORK=off go build` (the Dockerfile) work.
* Dashboard: Next.js static export (`apps/dashboard`), served by nginx; `/api` is proxied to the gateway by Traefik (and by nginx as a fallback).
* Shared conventions: errors are `{"error":{"code","message"}}`; `/internal/*` routes are wrapped by `internalauth.Require`;
  service-to-service HTTP uses `internalauth.NewClient`; membership checks use `membership.Require/RequireProject`; request bodies are
  capped at 1 MiB (`httpx.Decode`, use `DecodeLimit` for uploads).

## Run it

```bash
scripts/jp-up.sh --dev --wait          # whole stack, dev defaults, datastores on 127.0.0.1 (see infra/compose/docker-compose.dev.yml)
scripts/jp-up.sh --dev --monitoring    # + Prometheus/Grafana/Loki/Tempo
JP_DEV=1 scripts/jp-compose.sh logs -f gateway
cd apps/dashboard && npm run dev       # hot reload against the gateway on :8000
go build ./... && go vet ./...         # inside any module directory
```

On Windows use `.\install.ps1` (generates a `.env`, starts the dev stack) and `.\scripts\jp-up.ps1 -Dev`.

## Adding things

* **A service:** `services/<name>` with its own `go.mod` (`replace` to `packages/go-common`), `cmd/main.go`, `migrations/`, `GET /healthz`,
  add it to `go.work`, to `infra/compose/docker-compose.yml` (anchors `*go`, `*env-common`, `*env-data`, `*env-urls`) and a route in the gateway.
  `scripts/verify-env.sh` fails if the service reads env vars compose does not set.
* **A migration:** add `NNN_name.sql` to the service's `migrations/` (forward-only; applied at start).
* **An add-on:** add an entry to the catalog (`services/addon/internal/catalog`) or drop JSON into `ADDON_CATALOG_DIR`; see [ADDONS.md](ADDONS.md).
* **A CLI command:** `apps/cli/cmd/<name>.go`.
* **Environment variable:** read it in Go, pass it in compose, document it above its line in `.env.example`, then
  `scripts/gen-env-docs.sh > docs/ENVIRONMENT.md`.

## Building images by hand

```bash
docker buildx build -f infra/docker/Dockerfile.service --build-arg SERVICE=gateway --platform linux/amd64,linux/arm64 .
docker buildx build -f infra/docker/Dockerfile.worker --platform linux/amd64,linux/arm64 .
```
