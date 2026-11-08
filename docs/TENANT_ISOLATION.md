# Tenant isolation model

"Tenant" = a project's application container(s) and its add-on / managed-database containers. Isolation is **container-level on a
shared kernel**; it is meant to stop accidents and ordinary misbehaviour, not a determined attacker with a kernel exploit
(see [SECURITY.md](SECURITY.md)).

## Network

* Each project gets a private bridge network `jp_p_<first 8 hex chars of the project UUID>` created by the `runtime` service.
  The project's app and add-on containers run **only** on it. Add-ons are reachable as `addon-<slug>` on that network.
* Tenant containers are **never** attached to the platform network `jp`, so they cannot reach Postgres, Redis, MinIO, the registry or
  any platform service by name or by IP. Two projects cannot see each other (different bridge networks; Docker's inter-bridge isolation).
* Traefik is attached to every project network (`docker network connect`) to reach the app; it publishes nothing from add-ons
  (add-on containers carry no `traefik.enable`).
* Outbound internet is open (no egress filtering). Host-published ports bound to `127.0.0.1` are not reachable from tenant networks.

## Container hardening (applied by runtime to every tenant container)

| Setting | Value |
|---|---|
| Capabilities | `cap_drop: ALL`, only `NET_BIND_SERVICE` added back |
| `no-new-privileges` | on |
| Privileged / host network / host PID / host mounts / `docker.sock` | never; image references and options are validated |
| PID limit | 512 |
| Memory / CPU | from `jp.yaml` `resources` (default 512 MB / 1 CPU), capped by `RUNTIME_MAX_MEMORY_MB` / `RUNTIME_MAX_CPUS` |
| Root filesystem | writable (applications need it) with `/tmp` as tmpfs |
| Logs | `json-file`, 10 MB x 3 files |
| Restart policy | per `jp.yaml` |
| Labels | `jp.managed=true`, `jp.org`, `jp.project`, `jp.deployment`, `jp.kind` + Traefik service port only. **No router labels**: routers come exclusively from the domain service. |

The user inside the container is whatever the image defines; JP does not force a non-root user because many images require
root at start. Capability drop and `no-new-privileges` still apply to root inside the container.

## Data

* Environment secrets are fetched from the secret service per deployment (`production` or `preview` set; previews never see production).
* Add-on and managed-database data live in named Docker volumes (`jp-addon-<project8>-<slug>`, ...), removed on delete only with purge.
* Storage buckets are MinIO buckets namespaced per project, accessed only through the storage service with membership checks.
* The platform database is not shared with tenants: managed databases are separate containers.

## Builds

Builds run in the host Docker daemon (via the worker), **not** inside tenant isolation: `RUN` steps have network access and
consume host CPU/disk without a per-build limit. Timeouts (default 15 min build / 3 min clone), output and size caps apply.

## What you should assume

A tenant can: use all CPU/memory up to its limit, fill the disk with writes/volumes/images, make outbound connections,
read its own environment variables. A tenant with a container-escape exploit owns the host. Do not host mutually hostile
tenants on one JP server.
