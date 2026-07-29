# jp Cloud Platform

Greenfield cloud platform monorepo. Brand / CLI name: **`jp`**.

Stack: Go microservices, PostgreSQL, Redis, Traefik, Next.js dashboard.

**Resources are adaptive** — a 4GB VPS is the *small* tier for demos; on larger hosts jp raises memory limits, worker replicas, and concurrency automatically (`scripts/size-host` + `infra/compose/sizing/`).

## Unified quickstart

### 1. Environment

```powershell
cd "d:\projects\cloud plateform"
Copy-Item .env.example .env   # skip if .env already exists
```

### 2. Detect host size and start (recommended)

```powershell
.\scripts\size-host.ps1                 # → .env.resources (JP_SIZE, WORKER_REPLICAS, …)
.\scripts\jp-up.ps1 -Profiles platform,ui
# or force a tier:  .\scripts\jp-up.ps1 -Size large -Profiles platform,ui,data,monitoring
```

```bash
./scripts/size-host.sh
./scripts/jp-up.sh platform,ui
```

| Tier | Host RAM (auto) | Workers | Concurrent builds / worker |
|------|-----------------|---------|----------------------------|
| small | &lt; 6 GB | 1 | 1 |
| medium | 6–16 GB | 2 | 2 |
| large | 16–32 GB | 4 | 2 |
| xlarge | 32 GB+ | 8 | 4 |

Manual Compose (without helpers) still works — see below.

### 2b. Manual Compose (optional)

Requires [Docker Desktop](https://www.docker.com/products/docker-desktop/) running.

**Core (Phase 1):**

```powershell
docker compose -f infra/compose/docker-compose.yml --env-file .env up -d --build
```

**Platform + scale workers (example: large box):**

```powershell
docker compose -f infra/compose/docker-compose.yml -f infra/compose/sizing/large.yml `
  --env-file .env --profile platform up -d --build --scale worker=4
```

**Phase 6 data / monitoring:**

```powershell
docker compose -f infra/compose/docker-compose.yml --env-file .env --profile data up -d --build
docker compose -f infra/compose/docker-compose.yml --env-file .env --profile monitoring up -d
```

| Endpoint | URL |
|----------|-----|
| Gateway health | http://localhost:8000/healthz |
| API base | `http://localhost:8000/api/v1` |
| Traefik dashboard | http://localhost:8080 |
| Docker registry | http://localhost:5000 (platform profile) |
| MinIO API / console | http://localhost:9000 / http://localhost:9001 (`data` profile) |
| Prometheus | http://localhost:9090 (monitoring) |
| Grafana | http://localhost:3001 (monitoring; admin/admin) |
| Loki | http://localhost:3100 (monitoring) |
| Tempo | http://localhost:3200 (monitoring) |

### 3. Dashboard (UI)

```powershell
docker compose -f infra/compose/docker-compose.yml --env-file .env --profile ui up -d --build
```

Or local Next.js: `cd apps\dashboard` → `npm install` → `npm run dev` (http://localhost:3000).

### 4. CLI (`jp`)

```powershell
cd apps\cli
# If Go is on PATH:
go build -o jp.exe .
# Or via Docker:
docker run --rm -v "${PWD}/../..:/src" -w /src/apps/cli golang:1.22-alpine go build -o jp.exe .
.\jp.exe login
.\jp.exe org use <slug|id>
.\jp.exe deploy --project <project-id>
.\jp.exe status --project <project-id>
.\jp.exe builds --project <project-id>
.\jp.exe domains list --project <project-id>
.\jp.exe domains add app.example.com --project <project-id>
.\jp.exe domains verify <domain-id> --project <project-id> --force
.\jp.exe secrets list --project <project-id> --env development
.\jp.exe secrets set API_KEY s3cret --project <project-id> --env development
.\jp.exe env list --project <project-id>
.\jp.exe env set DATABASE_URL postgres://... --project <project-id> --env production
.\jp.exe logs --project <project-id> --source runtime
.\jp.exe metrics --project <project-id>
.\jp.exe storage bucket --project <project-id>
.\jp.exe storage put notes.txt "hello" --project <project-id>
.\jp.exe storage ls --project <project-id>
.\jp.exe db create app --project <project-id>
.\jp.exe db list --project <project-id>
.\jp.exe apply --project <project-id>          # validate + apply jp.yaml
.\jp.exe deploy --project <project-id>         # reads jp.yaml when present
.\jp.exe ai "why did my build fail?" --project <project-id>
```

## Compose profiles

| Profile | What starts |
|---------|-------------|
| *(default / core)* | traefik, gateway, identity, organization, project, notification, postgres, redis |
| `platform` | Git/deploy/build/runtime/domain/certificate + **secret, logging, metrics** |
| `ui` | dashboard (http://localhost:3000) |
| `dev` | MailHog |
| `monitoring` | Prometheus, Grafana, Loki, Tempo, Promtail (optional; ~1GB extra) |
| `data` | **MinIO**, **storage** (8017), **database** (8018), **addon** (8021) |
| `addons` | Shared brokers for `ADDON_MODE=shared`: MySQL, Mongo, Redis `:6380`, RabbitMQ, Redpanda |

```powershell
docker compose -f infra/compose/docker-compose.yml --env-file .env --profile platform --profile data --profile ui up -d --build
```

Memory: **not fixed at 4GB**. Baseline Compose limits are soft; `infra/compose/sizing/{tier}.yml` raises Postgres/Redis/worker budgets with host size. Use `scripts/jp-up.*` so worker `--scale` and `WORKER_CONCURRENCY` match the machine. On **small** (~4GB) keep `monitoring` off unless demonstrating.

## What is real vs simulated (Phase 2–7)

Defaults are **real** (`scripts/go-real.*` / `.env`). Simulate only if you explicitly set `*_MODE=simulate`.

| Piece | Behavior |
|-------|----------|
| GitHub OAuth/install | **Real GitHub App** when `GITHUB_APP_ID` + key set; else **stub** |
| Repo list | Installation repos (App), or `GITHUB_TOKEN`, or mock |
| Webhooks / deploys / rollback / build queue | **Real** (push + pull_request previews) |
| Commit statuses | **Real** via App installation token; no-op without App |
| Worker build | **Docker BuildKit** (`BUILD_MODE=docker`) |
| Image push | Host Docker → `localhost:5000` (add insecure-registries) |
| Runtime | **Docker Engine** (`RUNTIME_MODE=docker`) |
| Domains / certs | Real DNS verify; ACME when `TRAEFIK_CERT_RESOLVER` set |
| Secrets / envs / logs | **Real** |
| Metrics | **`live`**; Prometheus via `--profile monitoring` |
| Storage / DB | **MinIO** + **schema** Postgres (`--profile data`) |
| AI | Heuristic without key; OpenAI API with `OPENAI_API_KEY` |

```powershell
.\scripts\go-real.ps1
```

## Phase roadmap (summary)

| Phase | Scope |
|-------|--------|
| **0** | Monorepo, shared packages, Compose, Traefik |
| **1** | Auth, orgs, projects, PATs, gateway, notifications |
| **2** | Repos, webhooks, deployments, rollback, dashboard/CLI |
| **3** | Build farm, worker, registry:2, image metadata |
| **4** | Runtime, scheduler, domains, certificates |
| **5** | Secrets, environments, logs, metrics, optional monitoring stack |
| **6** | Storage (MinIO), databases, cleanup queues, cron jobs |
| **7** | jp.yaml IaC, deploy strategies, AI ops, billing |
| **8** | Scale-out path documented ([docs/scale-out.md](docs/scale-out.md)) |

See [docs/full-platform-reference.md](docs/full-platform-reference.md) (complete architecture / endpoints / diagrams), [docs/phases.md](docs/phases.md), and [docs/vps-runbook.md](docs/vps-runbook.md).

## API contract

Base: `http://localhost:8000/api/v1`  
Auth: `Authorization: Bearer <access_token>`  
Full paths: [`packages/openapi/openapi.yaml`](packages/openapi/openapi.yaml).

### Phase 2–4 endpoints (gateway)

- `POST/GET /orgs/{orgId}/github/*` — install stub, list installations/repos
- `POST/GET/DELETE /orgs/{orgId}/projects/{projectId}/repos`
- `POST /webhooks/github` — public webhook
- `POST/GET /orgs/{orgId}/projects/{projectId}/deployments`
- `POST .../deployments/rollback` and `.../deployments/{id}/rollback`
- `GET .../builds`, `.../builds/{id}`, `.../builds/{id}/logs`
- `GET .../images`
- `GET/POST .../runtime/instances`, `.../start`, `.../stop`, `GET .../runtime/containers`
- `GET/POST/DELETE .../domains`, `POST .../domains/{id}/verify`
- `GET .../certificates`, `POST .../certificates/{id}/renew`

### Phase 5 endpoints (gateway)

- `GET/POST .../environments`
- `GET/POST/PUT/DELETE .../environments/{env}/secrets[/{name}]`
- `GET/PUT/DELETE .../env/{env}[/{name}]` — convenience aliases
- `GET/POST .../logs`, `POST .../logs/ingest`
- `GET/POST .../metrics`, `GET/POST .../metrics/targets`

### Phase 6 endpoints (gateway)

- `GET/POST .../storage/bucket`
- `GET/POST .../storage/objects` — list / upload (`data_base64`)
- `POST .../storage/signed-url` — `{ key, expires? }`
- `DELETE .../storage/objects?key=`
- `GET/POST/DELETE .../databases[/{dbId}]`
- `GET/POST/DELETE .../cron[/{cronId}]` — schedules that trigger runtime jobs
- `GET .../queues` — platform queue stub

### Phase 7 endpoints (gateway)

- `PUT/POST/GET .../config`, `GET .../config/drift` — jp.yaml desired state
- `POST .../ai/explain`, `POST /orgs/{orgId}/ai/ask`, `GET /orgs/{orgId}/ai/status`
- `GET /billing/plans`, `GET/POST .../billing/usage|events|plan`

## Layout

```
packages/go-common   Shared Go helpers (+ otelx, jpconfig)
packages/events      Event envelope + Redis Streams helpers
packages/openapi     OpenAPI 3 (Phase 1–7)
packages/jp-schema   jp.yaml JSON Schema
services/*           Microservices (incl. ai 8019, billing 8020)
infra/compose        Docker Compose
infra/monitoring     Prometheus / Loki / Tempo / Grafana / Promtail configs
infra/traefik/dynamic  Traefik file provider (gateway + per-domain routers)
apps/dashboard       Next.js console
apps/cli             jp CLI (`jp deploy`, `jp apply`, `jp ai`, …)
docs/                Runbook, roadmap, scale-out
```
