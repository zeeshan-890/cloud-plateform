# Operations

All commands run from the checkout (default `~/cloud-plateform`). `scripts/jp-compose.sh` is `docker compose` with the correct
`-f`/`--env-file` arguments for this install.

## Ports

| Port | Bind | What | Public? |
|---|---|---|---|
| 80, 443 | `0.0.0.0` (`HTTP_BIND`/`HTTPS_BIND`) | Traefik | yes |
| 3000 | `127.0.0.1` (`0.0.0.0` with `--ip-only`) | dashboard (nginx) | only in `--ip-only` |
| 8000 | `127.0.0.1` | gateway (used by health checks) | no |
| 8080 | `127.0.0.1` | Traefik dashboard, basic auth (`admin` / `TRAEFIK_DASHBOARD_PASSWORD` in `.env`) | no, SSH tunnel |
| 5000 | `127.0.0.1` | image registry (no auth; the host Docker daemon pushes/pulls here) | no |
| 9090 / 3001 / 3100 / 3200 / 4317 / 4318 | `127.0.0.1` | Prometheus / Grafana / Loki / Tempo / OTLP (monitoring profile) | no |
| 5432, 6379, 9000, 9001 | not published | Postgres, Redis, MinIO API/console | no (dev override publishes on `127.0.0.1`) |
| 8001-8021 | not published | Go services on the `jp` network | no |

Use SSH tunnels for the loopback ports: `ssh -L 8080:127.0.0.1:8080 server`.

## Day to day

```bash
scripts/jp-compose.sh ps
scripts/jp-compose.sh logs -f --tail=100 gateway
scripts/jp-compose.sh restart runtime
scripts/jp-up.sh --monitoring            # start/enable Prometheus, Grafana, Loki, Tempo, Promtail (persisted)
scripts/jp-up.sh --no-monitoring
scripts/size-host.sh medium              # force a sizing tier, then scripts/jp-up.sh
docker ps --filter label=jp.managed=true # all tenant containers
```

Grafana: set `GRAFANA_DOMAIN` to route it through Traefik (its own login protects it; anonymous access is off) or tunnel to
`127.0.0.1:3001`. Prometheus scrapes Prometheus itself, Traefik (`:8082`) and the JP metrics service; other services expose health only.

## Sizing

`scripts/size-host.sh` picks `small`/`medium`/`large`/`xlarge` from RAM and writes `.env.resources` (worker replicas and concurrency +
the overlay in `infra/compose/sizing/`). Limits are cgroup caps for platform containers; tenant containers have their own limits and
builds run in the host daemon outside these caps. Details: `infra/compose/sizing/README.md`.

## Upgrade, backup, restore

[BACKUP_RESTORE.md](BACKUP_RESTORE.md). `scripts/upgrade.sh` takes a backup, pulls, builds, restarts services one by one waiting for
health, and rolls back code and images if one fails. Database migrations are forward-only: a rollback restores previous *code*; if a
migration is incompatible, restore the pre-upgrade backup.

## Rotating secrets

| Secret | How |
|---|---|
| `JWT_SECRET` | change in `.env`, `scripts/jp-compose.sh up -d` - all users are logged out |
| `INTERNAL_TOKEN` | change in `.env`, recreate all services (`up -d`) |
| `POSTGRES_PASSWORD`, `REDIS_PASSWORD` | change inside the datastore first (`ALTER USER`, `CONFIG SET requirepass`), then `.env`, then `up -d` |
| `SECRETS_MASTER_KEY` | rotation is supported by the secret service: set `SECRETS_KEY_VERSION=2`, move the old key to `SECRETS_MASTER_KEY_PREVIOUS`, put the new key in `SECRETS_MASTER_KEY`, `up -d secret`, POST the internal endpoint `/internal/rotate-master-key` (with `X-Internal-Token`, from inside the `jp` network) to re-encrypt rows, then remove `*_PREVIOUS`. Never just replace the key: stored secrets become unreadable |
| Traefik dashboard | `openssl passwd -apr1` -> `TRAEFIK_DASHBOARD_AUTH` (single quotes!) |

## Troubleshooting

| Symptom | Look at |
|---|---|
| container restarting | `scripts/jp-compose.sh logs --tail=80 <svc>`; most often a missing/invalid env value (services refuse to start in production without `INTERNAL_TOKEN`/strong `JWT_SECRET`) |
| gateway returns 502 `{"error":...}` | the upstream service named in the message is down: `ps`, then its logs |
| deploy stuck at "building" | `logs worker`; Docker daemon reachable? (`DOCKER_GID`), disk full (`docker system df`) |
| deploy fails at health gate | the app must answer HTTP on its configured port within the timeout; old version keeps running |
| disk full | `docker system df`, `docker image prune -a --filter until=168h` (scheduler already prunes orphan images after `ORPHAN_IMAGE_TTL`), prune build cache `docker builder prune` |
| Traefik shows no router for an app | domain verified? project has a running deployment? `ls` of the `traefik_dynamic` volume |
| "network jp_p_xxxx not found" for Traefik | runtime reconciles on start; `restart runtime` |
| need a DB shell | `scripts/jp-compose.sh exec postgres psql -U jp jp` |
