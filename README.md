# JP Cloud

A self-hosted application platform (one control-plane server; projects can also run on additional servers): connect a Git repository, JP builds it in Docker, runs it in an isolated
container behind Traefik, gives it a hostname, TLS, secrets, a database and other add-ons. It is a set of Go microservices
(21), a Next.js dashboard, a `jp` CLI and a Docker Compose stack.

It runs on **one server** (Linux, x86_64 or arm64, >= 4 GB RAM). There is no multi-node scheduling, no Kubernetes and
no hosted offering. Read [docs/SECURITY.md](docs/SECURITY.md) before exposing it to people you do not trust: the
platform builds and runs customer code through the host Docker daemon, which is root-equivalent.

## Install (Linux server)

```bash
# with a domain: needs DNS  A panel.example.com  and  A *.apps.example.com  pointing at the server
curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/install.sh | \
  bash -s -- --domain apps.example.com --panel-domain panel.example.com --email you@example.com

# without a domain (plain http on the server IP, apps under <ip-with-dashes>.sslip.io)
curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/install.sh | bash -s -- --ip-only
```

The installer installs Docker (Oracle Linux/RHEL family, Debian, Ubuntu), generates every secret into a `chmod 600` `.env`,
opens ports 80/443 (3000 for `--ip-only`) in firewalld/ufw/iptables - including Oracle Cloud's default `REJECT` rule - builds
the images, starts the stack and checks `/healthz`. It never overwrites an existing `.env`. The first account you register
becomes the platform administrator. Full guide: [docs/INSTALLATION.md](docs/INSTALLATION.md). Windows
(Docker Desktop, local development only): `.\install.ps1`.

## What it does

* Organizations and projects; owner/admin users create other users (generated one-time passwords) and give members access per project and per area ([docs/ACCESS_CONTROL.md](docs/ACCESS_CONTROL.md)); invitations, TOTP MFA, personal access tokens.
* GitHub (App or token) repositories, push-to-deploy webhooks, PR previews, `jp.yaml` project manifest.
* Continuous deployment per project: auto-deploy, production branch, wait for GitHub Actions CI, path filters, `[skip ci]`, and **monorepos** (several projects build different folders of one repository) ([docs/CONTINUOUS_DEPLOYMENT.md](docs/CONTINUOUS_DEPLOYMENT.md)).
* Builds with `docker build` in the worker (the repository's Dockerfile, or a generated one for detected layouts), Trivy image scan,
  images pushed to a local registry, rolling and blue/green deployments with a health gate and automatic rollback.
* Each project runs on its own private Docker network with hardened containers (see [docs/TENANT_ISOLATION.md](docs/TENANT_ISOLATION.md)).
* Auto-generated hostnames `<project>-<org>.<ROOT_DOMAIN>`, custom domains verified by real DNS lookups, Let's Encrypt
  certificates via Traefik ([docs/DOMAINS.md](docs/DOMAINS.md)).
* Encrypted per-environment secrets (AES-GCM envelope, master key in `.env`), production/preview separation.
* Add-ons (22 of the 241 catalog entries are provisionable) and managed databases as real containers with generated credentials, one per project
  ([docs/ADDONS.md](docs/ADDONS.md)); object storage buckets on MinIO.
* Logs, metrics, AI-assisted failure explanations (Gemini and OpenAI-compatible providers, bring your own key).
* Optional monitoring stack: Prometheus, Grafana, Loki, Tempo, Promtail.

What it does **not** do: multi-node clusters, autoscaling, managed backups of tenant data, payments or billing (there is none),
SSO, or a hardened multi-tenant boundary comparable to a VM per tenant.
[docs/FEATURE_STATUS.md](docs/FEATURE_STATUS.md) lists every feature with the test that verifies it.

## Layout

| Path | Contents |
|---|---|
| `services/*` | 21 Go microservices, each its own module (`gateway` is the only public API) |
| `packages/go-common` | shared auth, HTTP, middleware, internal-token client, membership checks, crypto helpers |
| `packages/events` | Redis Streams event bus |
| `apps/dashboard` | Next.js static export served by nginx |
| `apps/cli` | the `jp` command line client |
| `infra/compose` | production compose file, sizing overlays, dev and e2e overrides |
| `infra/docker` | Dockerfiles (`Dockerfile.service` for all services, `Dockerfile.worker`) |
| `scripts` | installer helpers, backup/restore/upgrade, env verification |
| `e2e` | black-box test suite run against a deployed server |
| `docs` | documentation |

## Operate

```bash
scripts/jp-compose.sh ps                 # docker compose with this install's files and env
scripts/jp-up.sh --monitoring            # add Prometheus/Grafana/Loki/Tempo
scripts/backup.sh --keep 7               # database + MinIO + .env + certificates, verified
scripts/restore.sh backups/jp-backup-... # restore (destructive)
scripts/upgrade.sh                       # pull, build, restart in order, roll back on failure
./uninstall.sh [--purge-data]            # remove containers (and optionally all data)
```

Develop locally: `scripts/jp-up.sh --dev` (Linux/macOS) or `.\install.ps1` (Windows); tests: `scripts/test-all.sh`
([docs/TESTING.md](docs/TESTING.md), [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)).

## Documentation

[Installation](docs/INSTALLATION.md) | [Architecture](docs/ARCHITECTURE.md) | [Operations](docs/OPERATIONS.md) |
[Backup & restore](docs/BACKUP_RESTORE.md) | [Security & threat model](docs/SECURITY.md) |
[Access control](docs/ACCESS_CONTROL.md) | [Tenant isolation](docs/TENANT_ISOLATION.md) | [Roadmap](docs/ROADMAP.md) | [Domains](docs/DOMAINS.md) | [Add-ons](docs/ADDONS.md) |
[Environment variables](docs/ENVIRONMENT.md) | [Testing](docs/TESTING.md) | [Development](docs/DEVELOPMENT.md) |
[Release](docs/RELEASE.md) | [Feature status](docs/FEATURE_STATUS.md)

## License

No license file is included in the repository yet.
