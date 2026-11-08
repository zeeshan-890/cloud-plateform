# Security model and threat model

This document states what JP protects, how, and - just as important - what it does **not** protect. Read it before letting
untrusted people deploy code on your server.

## Assets and actors

| Asset | Why it matters |
|---|---|
| Host root | the platform needs the Docker socket; whoever controls it controls the host |
| `.env` (`SECRETS_MASTER_KEY`, `JWT_SECRET`, `INTERNAL_TOKEN`, DB/Redis passwords) | compromise = full platform compromise |
| Platform Postgres | users, organizations, encrypted secrets, domains |
| Tenant secrets / data | per-project secrets, add-on and database volumes, MinIO buckets |
| Other tenants' workloads | confidentiality and availability between projects |

Actors: anonymous internet users; registered users (low-trust, may deploy arbitrary code); org admins; the platform admin
(first account, trusted); a compromised tenant container (assume attacker-controlled code runs in it).

## Controls

**Edge.** Only Traefik publishes 80/443 (and the dashboard on 3000 in `--ip-only` mode). Traefik's API is *not* insecure: the
dashboard is a router on a separate entrypoint bound to `127.0.0.1:8080` behind basic auth (use an SSH tunnel). TLS via ACME is
enabled only when `TLS_ENABLED=true`; the HTTP->HTTPS redirect exists only then. Traefik reads Docker through `docker-socket-proxy`
(containers + networks, GET only) instead of mounting the socket, with `exposedbydefault=false`. The `traefik_dynamic` volume is read-only for Traefik and
writable only by `domain` and `certificate`.

**Exposure.** Postgres, Redis, MinIO, Prometheus, Grafana, Loki, Tempo, the registry and the gateway publish only on
`127.0.0.1` (most not at all in production). Redis requires a password; MinIO and Postgres credentials are random and required.
`infra/compose/docker-compose.yml` contains no default secrets - compose fails if one is missing.

**Authentication / authorisation.** Short-lived JWTs, rotating refresh sessions, TOTP MFA, hashed PATs, login rate limiting,
token revocation list in Redis. Every project-scoped endpoint verifies org membership **and** that the project belongs to the org
(`membership.RequireArea`, fail closed). Members see only granted projects and areas (see [ACCESS_CONTROL.md](ACCESS_CONTROL.md)).
Registration is `invite` by default after the first admin. Service-to-service endpoints require `X-Internal-Token`.

**Secrets.** AES-256-GCM envelope encryption in the `secret` service; list endpoints return metadata and a hint only; plaintext
reveal needs admin and writes an audit row. Runtime obtains the merged environment through an internal endpoint per deployment;
preview deployments receive only `preview`/`all` secrets, never `production`. The master key exists in exactly the secret service
(and the identity/ai services use their own separate keys).

**Hostnames and routing.** Hostnames are validated with a strict RFC 1123 regex before any use; router files are produced with
a YAML marshaller (no string concatenation) and written atomically; only verified domains get routers or certificates.

**Webhooks and git.** Per-repository random webhook secrets (HMAC-SHA256), clone URLs restricted to https (or ssh with a deploy
key), host must resolve to public addresses, no `-` option injection, tokens passed via `GIT_ASKPASS` never embedded in URLs, GitHub
App callbacks verify a single-use `state`. The worker image pins github.com's SSH host keys (`infra/docker/github_known_hosts`).

**Containers (platform).** Go services run as uid 10001, read-only root filesystem, `cap_drop: ALL`, `no-new-privileges`, JSON log rotation,
memory/CPU limits and `GOMEMLIMIT`. Images are built from pinned base images for amd64 and arm64.

**Containers (tenants).** See [TENANT_ISOLATION.md](TENANT_ISOLATION.md).

## The Docker socket (the big risk)

`runtime` and `worker` mount `/var/run/docker.sock`. Any process that can write to that socket can start a privileged container
that mounts `/` - i.e. it is **root on the host**. Mitigations in place: both run as a non-root uid with only the socket's group;
read-only root filesystems; no cap; they accept no code from tenants (tenant code runs in other containers, built by the daemon);
all their inputs are validated (image references, names, resource ceilings `RUNTIME_MAX_*`); `/internal/*` needs the internal token.
Residual risk: a remote-code-execution bug in `runtime` or `worker`, a leaked `INTERNAL_TOKEN`, or a malicious `Dockerfile`
exploiting a Docker/BuildKit vulnerability gives host root. Keep Docker updated, keep the host dedicated to JP, and do not run JP
for hostile tenants without additional isolation (gVisor/Kata/separate VMs), which JP does not provide.

## What is NOT protected

* **Kernel-level isolation.** Tenants share the host kernel. A container escape (kernel/runc CVE) compromises everything.
* **Build-time attacks.** `docker build` executes tenant `RUN` steps with network access. They can attack your LAN/metadata
  services (169.254.169.254) and consume CPU/disk: builds are not egress-filtered and not covered by the compose memory limits.
* **Resource exhaustion by tenants** beyond their per-container limits (disk in volumes/images, inode use, build cache, outbound bandwidth). Quotas for storage exist; disk quotas for images/volumes do not.
* **Tenant egress.** Tenant containers have unrestricted outbound internet access and can reach whatever the host network
  can reach (including cloud metadata endpoints). JP adds no egress filtering; add host firewall rules if that matters.
  Loopback-only services (`127.0.0.1:...`) are not reachable from container networks, but do not rely on that for secrets.
* **Secrets in process environment.** Project secrets are injected as container environment variables: anyone with `docker inspect` rights on the host or code execution in that container sees them.
* **`INTERNAL_TOKEN` is one shared secret** for all services; a compromised service can call every other `/internal/*` endpoint.
* **The monitoring stack** (Prometheus, Loki, Grafana) is single-tenant admin tooling; Loki holds container logs of all projects.
* **Backups** contain `.env` in plaintext unless `BACKUP_PASSPHRASE` is set.
* **Availability**: single node, no HA; the Postgres/Redis/MinIO containers are single instances.
* **Supply chain**: images are pinned by tag, not by digest; Trivy scans tenant images when `TRIVY_ENABLED=true` and its vulnerability DB is reachable (HIGH and CRITICAL findings are reported on the build).

## Hardening checklist for operators

1. Keep `.env` at `chmod 600`, store a copy of `SECRETS_MASTER_KEY` offline.
2. Use `REGISTRATION_MODE=invite` or `closed`; protect the first-admin step with `BOOTSTRAP_TOKEN`.
3. Enable TLS (`--domain`) and set `HSTS_SECONDS=31536000` once it works.
4. Restrict cloud security groups to 80/443 (and 22 from your IP).
5. Run `scripts/backup.sh` on a schedule and test `scripts/restore.sh`.
6. Update regularly (`scripts/upgrade.sh`); watch the `govulncheck` CI job.
7. Treat the platform admin account as root of the server.

Reporting vulnerabilities: open a private security advisory on the GitHub repository.
