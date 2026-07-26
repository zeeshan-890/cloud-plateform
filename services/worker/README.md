# Worker — Phase 3 build farm consumer

Claims jobs from Redis Streams (`jp.build` / `jp-workers`).

| Env | Default | Meaning |
|-----|---------|---------|
| `WORKER_CONCURRENCY` | `1` | Parallel builds **inside** one process |
| `WORKER_NAME` | container hostname | Unique consumer id (set automatically when scaled) |
| `BUILD_MODE` | `simulate` / sized by host | `docker` for real BuildKit |

Scale replicas with Compose: `docker compose ... --scale worker=N` (or `scripts/jp-up.*`).
Host sizing overlays set concurrency + mem/cpu limits — see `infra/compose/sizing/`.

Claims jobs from Redis Stream `jp.build`, simulates or runs BuildKit builds, updates build + deployment status.
