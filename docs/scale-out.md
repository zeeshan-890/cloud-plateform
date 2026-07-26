# Scale-out path (Phase 8 — planned)

This document describes how jp grows beyond a **single Compose host**. Phases 1–7 already **scale vertically** via `scripts/size-host` + `infra/compose/sizing/` (more RAM → more workers, higher limits). Phase 8 is **horizontal**: multiple machines / Kubernetes / Firecracker.

## Current constraints (Phase 1–7 on one host)

| Component | Today |
|-----------|--------|
| Scheduler | One slot (`node-1`) — multi-node placement is Phase 8 |
| Build workers | `WORKER_REPLICAS` × `WORKER_CONCURRENCY` sized from host RAM |
| Runtime | Docker Engine on the host, or `RUNTIME_MODE=simulate` |
| Networking | Traefik file provider + Compose network |
| State | Shared Postgres + Redis Streams |
| Limits | Compose overlays: small / medium / large / xlarge |

## Target architecture

```
                    ┌──────────────┐
   Clients ────────►│  Traefik /   │
                    │  API gateway │
                    └──────┬───────┘
                           │
        ┌──────────────────┼──────────────────┐
        ▼                  ▼                  ▼
   control plane      data plane         build farm
   (identity, org,    (runtime nodes     (N workers
    deploy, billing,   or Firecracker     claiming
    ai, …)             microVMs)          jp.build)
```

## Kubernetes direction

1. **Control plane** — Deploy existing Go services as Deployments; keep Postgres/Redis as managed or StatefulSets.
2. **Gateway** — Replace Compose Traefik labels with Ingress / Gateway API; keep `/api/v1` contract.
3. **Scheduler** — Evolve from `node-1` to a placement controller: pick node/pool by capacity, region, and `jp.yaml` `deploy.region` / replicas.
4. **Runtime** — Prefer **Pods** (or Knative/Revision) instead of host Docker socket; map `rolling` → RollingUpdate, `blue_green` → dual Services + weighted Ingress (today’s Traefik flip stub).
5. **Workers** — HorizontalPodAutoscaler on build workers; Redis Streams consumer groups already support N consumers (`jp-workers`).
6. **Registry** — External OCI registry (or in-cluster Harbor) instead of Compose `registry:2`.

## Multi-worker builds

- Increase worker replicas; keep **one job per consumer message**.
- Partition by org/project if noisy neighbors appear.
- Move BuildKit to remote builders (or Kaniko/buildah) so workers stay thin.

## Firecracker / microVM path

For stronger isolation than containers:

1. Keep **control plane** on K8s or VMs.
2. Runtime agent on bare metal launches Firecracker VMs per instance (image → rootfs).
3. Traefik/Envoy routes to VM tap interfaces; health checks replace today’s Docker health stub.
4. Blue/green becomes two VM generations + LB weight flip (same API `strategy` field).

## Migration steps (suggested order)

1. Multi-worker Compose (`worker` scale=2+) while still single node — proves stream fan-out.
2. Externalize Postgres/Redis.
3. Move control-plane services to K8s; leave runtime on Docker node agent.
4. Replace runtime Docker with Pods or Firecracker agent.
5. Add multi-region slots and billing metering that is no longer stubbed.

## Non-goals for Phase 8 doc

- Full Helm charts / operators in this repo (yet)
- Multi-tenant hard isolation guarantees
- Replacing Redis Streams (Kafka later if needed)
