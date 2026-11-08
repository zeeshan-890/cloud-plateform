# Architecture

JP runs its control plane on one Docker host (additional servers can host whole projects, see [MULTI_SERVER.md](MULTI_SERVER.md)); that host runs the control plane (21 Go services + Postgres + Redis), the edge (Traefik),
supporting stores (MinIO, image registry) and the tenants' containers. Everything is created by
`infra/compose/docker-compose.yml`; tenant resources are created at runtime by the `runtime` service through the Docker API.

```
 internet --> Traefik :80/:443 --+--> /api/*   -> gateway :8000 --> 20 internal services (X-Internal-Token + JWT)
                                 +--> /       -> dashboard :3000 (nginx, static Next.js export)
                                 +--> Host(app) -> tenant containers on their own jp_p_<id> networks
```

## Services

All listen on the compose network `jp`; only the gateway is reachable from the edge. Every service exposes `GET /healthz`
and runs migrations from its own `migrations/` directory against the shared Postgres database at startup.

| Service | Port | Responsibility |
|---|---|---|
| gateway | 8000 | the only public API: JWT/PAT authentication, rate limits, CORS, request routing, platform settings, system pages |
| identity | 8001 | users, sessions, MFA (TOTP), personal access tokens, bootstrap/registration policy |
| organization | 8002 | organizations, members, roles, invitations |
| project | 8003 | projects, `jp.yaml` desired state, project delete orchestration |
| notification | 8004 | e-mail (SMTP) and in-app notifications |
| repository | 8005 | GitHub App/token integration, webhooks, deploy keys |
| deployment | 8006 | deployment records, strategies (rolling / blue-green), previews |
| build | 8007 | build records and logs, queue producer |
| worker | 8008 | consumes build jobs: `git clone`, `docker build`, push, Trivy scan |
| registry (`registry-api`) | 8009 | image metadata; the image store itself is the `registry:2` container |
| runtime | 8010 | creates/stops tenant containers, per-project networks, health gate, add-on containers |
| scheduler | 8011 | cron jobs, preview/orphan-image cleanup |
| domain | 8012 | hostnames, DNS verification, owns all Traefik routers (file provider) |
| certificate | 8013 | certificate status/probing; issuance itself is Traefik's ACME |
| secret | 8014 | encrypted secrets, the only holder of `SECRETS_MASTER_KEY` |
| logging | 8015 | build/runtime log storage and search |
| metrics | 8016 | metric ingestion/summary (reads Prometheus when the monitoring profile runs) |
| storage | 8017 | buckets/objects on MinIO, quotas |
| database | 8018 | managed databases (dedicated Postgres container per database) |
| ai | 8019 | failure explanations and ops assistant (Gemini and OpenAI-compatible providers) |
| addon | 8021 | add-on catalog (22 provisionable engines), provisioned as containers via runtime |

Supporting containers: `traefik`, `docker-socket-proxy` (read-only Docker API for Traefik/Promtail), `postgres`, `redis`
(password protected), `registry`, `minio`, `dashboard` (nginx), `init-volumes` (one-shot permission fix). Optional profile
`monitoring`: prometheus, grafana, loki, tempo, promtail.

## Trust boundaries and service-to-service auth

* Users and PATs authenticate at the gateway (short-lived JWT + refresh; claims include user id and platform-admin flag).
* The gateway forwards to services with the user's identity. Services authorise through `packages/go-common/membership`,
  which asks the organization service (fail closed) and enforces the access model: read = viewer+, create/update/deploy = member+,
  destructive or sensitive = admin+, delete org / change roles = owner.
* Service-to-service routes live under `/internal/*`, wrapped by `internalauth.Require` (constant-time check of the
  `X-Internal-Token` header against `INTERNAL_TOKEN`). In production a missing token makes the service refuse to start.
* Services never read each other's tables except through APIs. Tenant containers can reach none of this: they are not on `jp`.

## Networks

| Network | Members | Notes |
|---|---|---|
| `jp` | all platform containers, Traefik | bridge |
| `jp_socketproxy` | docker-socket-proxy, Traefik, Promtail | `internal: true` (no outbound) |
| `jp_p_<project8>` | one project's app + add-on containers, plus Traefik | created by runtime; Traefik is attached with `docker network connect` (container `jp-traefik`, `TRAEFIK_CONTAINER`) |

## Request flow: deploy

1. Push webhook (or dashboard/CLI) -> repository (applies the connection's CD rules: branch, folder/path filters, skip markers, wait-for-CI) -> deployment creates a record and asks `build`. The worker builds the connection's root directory ([CONTINUOUS_DEPLOYMENT.md](CONTINUOUS_DEPLOYMENT.md)).
2. `build` enqueues a job on Redis Streams; a `worker` replica (`WORKER_CONCURRENCY` jobs in parallel) clones the exact
   commit, builds with the host Docker daemon, pushes to `127.0.0.1:5000/<org>/<project>:<tag>`. With `TRIVY_ENABLED=true` (default) the worker scans the pushed image with Trivy and records the result (HIGH/CRITICAL findings) on the build.
3. `deployment` tells `runtime` to start the container on the project network. Secrets are fetched by runtime from
   the secret service's internal env endpoint per request - runtime never holds the master key.
4. Rolling: new container joins the same Traefik service (load-balanced), passes the HTTP health gate, then the old
   one drains and stops. Blue/green: the new colour gets its own service name; after the health gate runtime asks `domain`
   to atomically rewrite the routers to the new service, then the old colour is drained. A failed gate keeps the old
   version running and marks the deployment `failed`.
5. Traefik routes by `Host()` rules written only by the domain service to the file provider directory.

## Data stores

| Store | Used for | Persistence |
|---|---|---|
| Postgres 16 | all service tables | `postgres_data` volume |
| Redis 7 (`requirepass`, AOF, `noeviction`) | build queue streams, rate limits, token revocation | `redis_data` |
| MinIO | tenant storage buckets | `minio_data` |
| registry:2 (delete enabled) | built images | `registry_data` |
| Traefik | ACME state, generated routers | `traefik_acme`, `traefik_dynamic` |

Ports and exposure: [OPERATIONS.md](OPERATIONS.md#ports). Security properties and limits: [SECURITY.md](SECURITY.md).
