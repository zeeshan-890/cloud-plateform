# runtime service

Desired-state runtime for apps (`static` / `node` / `container`).

| Env | Default | Notes |
|-----|---------|-------|
| `PORT` | `8010` | HTTP |
| `RUNTIME_MODE` | `simulate` | `simulate` or `docker` |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Engine API socket |

When `RUNTIME_MODE=docker` and the Docker socket is unreachable, operations fall back to **simulate** with clear status (`mode: simulate`, container ids prefixed `sim-`).

## Routes

- `GET/POST /orgs/{orgId}/projects/{projectId}/runtime/instances`
- `POST .../runtime/instances/{id}/start|stop`
- `GET .../runtime/containers`
- `POST /internal/runtime/start` — used by scheduler after build/deploy success
