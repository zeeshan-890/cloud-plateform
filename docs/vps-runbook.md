# Host runbook (resource-adaptive)

## Philosophy

**4GB / 2 vCPU is the minimum demo tier (`small`), not the product limit.**  
jp sizes itself from available RAM and CPU: more memory → larger Postgres/Redis/worker budgets, more build workers, and optional monitoring/data profiles.

| Tier | Typical host | Workers | Concurrency / worker | Notes |
|------|--------------|---------|----------------------|--------|
| **small** | ~4 GB | 1 | 1 | Keep `monitoring` off unless demoing |
| **medium** | ~8–16 GB | 2 | 2 | platform + ui + data comfortable |
| **large** | ~16–32 GB | 4 | 2 | monitoring OK; real Docker builds |
| **xlarge** | 32 GB+ | 8 | 4 | full stack |

## Install

1. Install Docker Engine + Compose plugin (or Docker Desktop).
2. Clone this repo.
3. `cp .env.example .env` and set a strong `JWT_SECRET` / `POSTGRES_PASSWORD`.
4. **Detect host and start** (recommended):

```powershell
.\scripts\size-host.ps1          # writes .env.resources
.\scripts\jp-up.ps1 -Profiles platform,ui
```

```bash
./scripts/size-host.sh
./scripts/jp-up.sh platform,ui
```

Force a tier (e.g. on a beefy machine while testing tight limits):

```powershell
.\scripts\jp-up.ps1 -Size small -Profiles platform
.\scripts\jp-up.ps1 -Size large -Profiles platform,ui,data,monitoring
```

5. Verify:

```bash
curl -s http://127.0.0.1:8000/healthz
```

## How sizing works

1. `size-host` reads total RAM + logical CPUs → picks `JP_SIZE`.
2. Writes `.env.resources` (`WORKER_REPLICAS`, `WORKER_CONCURRENCY`, `BUILD_MODE`, …).
3. `jp-up` merges Compose files:
   - `infra/compose/docker-compose.yml`
   - `infra/compose/sizing/{tier}.yml` (mem/CPU overlays)
4. Scales build workers: `--scale worker=$WORKER_REPLICAS`.

Manual equivalent:

```powershell
docker compose `
  -f infra/compose/docker-compose.yml `
  -f infra/compose/sizing/large.yml `
  --env-file .env --env-file .env.resources `
  --profile platform --profile ui --profile data `
  up -d --build --scale worker=4
```

## Backups

- Postgres volume `postgres_data` — `pg_dump` nightly.
- Redis is cache/session; loss forces re-login (acceptable for early phases).
- MinIO (`data` profile) — back up the MinIO volume if you store production artifacts.

## Upgrades

```bash
docker compose -f infra/compose/docker-compose.yml -f infra/compose/sizing/$JP_SIZE.yml \
  --env-file .env --env-file .env.resources --profile platform pull
# then jp-up again
```

See also [scale-out.md](scale-out.md) for multi-node / Kubernetes.
