# Phase roadmap

## Phase 0 — Foundation

- Monorepo layout, `.gitignore`, `.env.example`, `go.work`
- `packages/go-common`, `events`, `openapi`, `jp-schema`
- Docker Compose + Traefik with **adaptive** resource overlays (`infra/compose/sizing/`)
- Docs / runbook

## Phase 1 — Identity & tenancy (implemented)

- **identity** (8001): register/login, JWT access+refresh (Redis), sessions, PATs, audit log
- **organization** (8002): orgs, invites, roles (`owner|admin|member|viewer`), members
- **project** (8003): CRUD projects under `org_id`
- **gateway** (8000): `/api/v1/*`, JWT validation, routing, org context headers
- **notification** (8004): accept/log invite emails (MailHog later via `dev` profile)

## Phase 2 — Git + deployments (implemented)

- **repository** (8005): GitHub OAuth/install stubs, mock (or `GITHUB_TOKEN`) repo list, connect/disconnect repos, webhook receiver with HMAC signature verification, emit events
- **deployment** (8006): create/list deployments; trigger from webhook push or API; status lifecycle `queued → building → ready|failed`; rollback creates a new deploy with `rollback_of` pointing at a previous successful deploy
- Gateway routes under `/api/v1` for git + deployments
- Dashboard: Connect Git, Deployments pages
- CLI: `jp deploy`, `jp status`, `jp rollback`, `jp builds`

## Phase 3 — Build farm (implemented)

- Redis Streams queue (`jp.build`) for build jobs
- **build** (8007): create job (internal), list/get status, fetch logs
- **worker** (8008): Redis Streams consumer; `WORKER_CONCURRENCY` parallel builds per process; scale replicas with `--scale worker=N` / `jp-up`; default build `BUILD_MODE=simulate` on small hosts, `docker` on medium+ when sized by `size-host`
- **registry-api** (8009): image metadata records
- **registry** (`registry:2` on :5000): container registry in Compose
- Compose `--profile platform` starts: repository, deployment, build, worker, registry-api, registry

## Phase 4 — Runtime, scheduler, domains, SSL (implemented)

- **runtime** (8010): desired-state instances (`static`/`node`/`container`); start/stop/list via Docker Engine API or `RUNTIME_MODE=simulate`
- **scheduler** (8011): single-node slot (`node-1`), consumes `jp.deploy` (`deploy.updated` when ready), health loop + restart policy, rolling-update stub → calls runtime
- **domain** (8012): add/list/delete domains; TXT/CNAME verify or force/`DOMAIN_DNS_STUB`; writes Traefik file-provider configs under `infra/traefik/dynamic/`
- **certificate** (8013): cert status + renew metadata; Traefik ACME resolver labels when configured (`CERT_SIMULATE=true` by default)
- After deploy+build succeeds → deployment publishes `deploy.updated` → scheduler auto-starts runtime from `image_ref`
- Gateway routes for runtime, domains, certificates
- Dashboard: Domains + Runtime pages; CLI: `jp domains`, `jp status` shows runtime
- Compose `--profile platform` also starts: runtime, scheduler, domain, certificate

## Phase 5 — Secrets, environments, logs, metrics, tracing (implemented)

- **secret** (8014): AES-256-GCM envelope encryption (`SECRETS_MASTER_KEY`); versioned secrets; never returns plaintext after create/update (only `value_hint`); audit table; CRUD by org/project + env (`development|preview|staging|production`)
- Environments attached to projects (auto-provisioned); list/set/unset via secrets or `/env/{env}` aliases
- **logging** (8015): ingest/query API for build + runtime logs (Postgres store; optional push to Loki when `LOKI_URL` set); can merge build-service logs for `source=build`
- **metrics** (8016): project summary API + `/metrics` Prometheus exposition; scrape-target annotations; `METRICS_MODE=simulate` seeds sample series
- Gateway propagates `X-Request-ID` / `X-Trace-ID` / `traceparent` (`packages/go-common/otelx`); Tempo under monitoring profile
- Gateway routes for secrets, env, logs, metrics
- Compose `--profile platform` also starts: secret, logging, metrics
- Compose `--profile monitoring` (optional): Prometheus, Grafana, Loki, Tempo, Promtail
- Dashboard: Secrets, Logs, Metrics pages
- CLI: `jp secrets`, `jp env`, `jp logs`, `jp metrics` (`jp trace` stays monitoring-dependent stub)

## Phase 6 — Storage, databases, queues, cron (implemented)

- **storage** (8017): buckets per org/project; upload (base64); signed URLs; list/delete; `STORAGE_MODE=simulate|minio` (falls back to simulate if MinIO down)
- **MinIO** on Compose `--profile data` only (`:9000` API, `:9001` console)
- **database** (8018): one-click Postgres — `DB_MODE=schema` creates schema+role on shared Postgres, or `simulate`; connection string stored via secret service (`secret_ref`)
- Platform queues via Redis Streams: `jp.build`, `jp.cleanup`, `jp.jobs` (+ user-facing `/queues` stub)
- **scheduler** cron API: create/list/delete schedules (`@hourly`, `@every 5m`, …) → publishes `jp.jobs` → triggers runtime jobs
- Cleanup jobs on `jp.cleanup`: orphaned image metadata + expired preview deploys (branch/message heuristic)
- Gateway routes for storage, databases, cron, queues
- Dashboard: Storage + Databases pages
- CLI: `jp storage`, `jp db`
- Memory: MinIO / storage / database scale with sizing overlays; use `--profile data` when the host can afford it (medium+)

## Phase 7 — AI ops, IaC (jp.yaml), advanced deploys, billing (implemented)

- **jp.yaml**: schema in `packages/jp-schema`; `jp apply` / `jp deploy` validate + POST desired state; project stores last-applied config + hash; drift stub on `GET .../config/drift`; dashboard shows hash/drift
- **Deploy strategies**: `rolling` (default — stop previous, start new) and `blue_green` (start alongside, Traefik flip stub, drain previous); rollback preserves/carries `strategy`
- **ai** (8019): tools fetch build logs, runtime logs, recent `jp.deploy` events; hosted LLM via `OPENAI_API_KEY` (+ `AI_BASE_URL` / `AI_MODEL`); **simulate** heuristic when no key; `POST .../ai/explain`, `POST .../ai/ask`; CLI `jp ai "..."`; dashboard Explain failure
- **billing** (8020): plans (`free`/`pro`/`scale`), usage events (`build_minutes`, `runtime_hours` stubs), summary API; consumes `jp.build` + `jp.deploy` streams
- Gateway routes; dashboard Billing + AI pages; OpenAPI + `.env.example` updated
- Compose `--profile platform` also starts: **ai**, **billing**

## Phase 8 — Scale-out path (documented, not implemented)

- See [docs/scale-out.md](scale-out.md): K8s / multi-worker / Firecracker direction without full K8s in this repo yet
