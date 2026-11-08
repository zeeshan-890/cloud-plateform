# jp-schema

`jp.schema.json` describes `jp.yaml` (read from the repository root by the build worker).

Honoured by the platform:

| Key | Effect |
|-----|--------|
| `runtime`, `build.dockerfile`, `build.context`, `build.command`, `build.env` | picks / confines the Dockerfile and build context (paths must stay inside the repo); `build.env` are plain build args, never secrets |
| `deploy.port`, `deploy.healthcheck`, `deploy.health_timeout_seconds`, `deploy.replicas`, `deploy.restart`, `deploy.strategy`, `deploy.env`, `env` | passed to the runtime: container port + `PORT` env, health gate path/timeout, replicas, restart policy, rolling or blue_green |
| `resources.memory`, `resources.cpus` | container limits (defaults 512MB / 1 CPU, clamped by the platform) |
| `security.block_on_critical` | fail the build when Trivy finds CRITICAL vulnerabilities (or cannot scan) |

Not in jp.yaml: `previews.allow_forks` is a per-repository setting chosen when the repository is connected (a fork can never be
trusted to enable itself), and fork previews never receive secrets.
