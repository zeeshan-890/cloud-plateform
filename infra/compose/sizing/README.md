# Resource overlays

`scripts/size-host.sh` reads the host RAM/CPU and writes `.env.resources` with `JP_SIZE`, `WORKER_REPLICAS`,
`WORKER_CONCURRENCY` and `COMPOSE_SIZING_FILE`. `scripts/jp-up.sh` then adds the matching overlay on top of
`infra/compose/docker-compose.yml`.

| Tier   | Host RAM  | Worker replicas x concurrency | Notes |
|--------|-----------|-------------------------------|-------|
| small  | < 6 GB    | 1 x 1                         | the base file's limits; leave monitoring off |
| medium | 6 - 16 GB | 1 x 2                         | Oracle A1 11 GB test box lands here |
| large  | 16 - 32 GB| 2 x 2                         | |
| xlarge | 32 GB+    | 2 x 4                         | |

Minimum supported host is 4 GB RAM / 2 vCPU (all 21 Go services + Postgres + Redis + MinIO + registry + Traefik run
by default; limits sum to ~5 GB but real use at idle is ~1.8 GB). Memory limits are cgroup caps; tenant
containers and `docker build` run in the host Docker daemon and are NOT covered by these limits - each tenant
container gets its own limit from `jp.yaml` (default 512 MB / 1 CPU).

Each overlay only changes `deploy.resources.limits` (and `GOMEMLIMIT` / `REDIS_MAXMEMORY` so the Go runtime and
Redis stay below their cgroup cap instead of being OOM-killed).
