# scheduler service

Single-node slot assignment, health-check loop, restart policy, rolling-update stub, **cron schedules**, and **cleanup jobs**.

## Streams

| Stream | Group | Purpose |
|--------|-------|---------|
| `jp.deploy` | `jp-scheduler` | Start runtime on `deploy.updated` (ready) |
| `jp.cleanup` | `jp-cleanup` | Orphan images + expired preview deploys |
| `jp.jobs` | `jp-jobs` | Cron-triggered runtime jobs |

## Cron API (via gateway `/api/v1`)

- `GET/POST /orgs/{orgId}/projects/{projectId}/cron`
- `DELETE /orgs/{orgId}/projects/{projectId}/cron/{cronId}`
- Expressions: `@hourly`, `@daily`, `@every 5m`, or `M H * * *`

## Queues stub

- `GET .../queues` — lists platform Redis Streams (user queues stub)

| Env | Default |
|-----|---------|
| `PORT` | `8011` |
| `RUNTIME_URL` | `http://runtime:8010` |
| `REGISTRY_URL` | `http://registry-api:8009` |
| `DEPLOYMENT_URL` | `http://deployment:8006` |
| `SCHEDULER_SLOT` | `node-1` |
| `CLEANUP_INTERVAL` | `1h` |
| `PREVIEW_TTL` | `72h` |
| `ORPHAN_IMAGE_TTL` | `168h` |
