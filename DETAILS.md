# Technical details

Pointers to the maintained documentation. This file intentionally contains no numbers that can go stale; run
`scripts/stats.sh` for line counts, route counts and module counts.

| Topic | Where |
|---|---|
| What JP is and is not | [README.md](README.md) |
| Service list, ports, networks, request flows | [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) |
| Installing, flags, `.env`, first login | [docs/INSTALLATION.md](docs/INSTALLATION.md) |
| Ports, day-to-day ops, secret rotation, troubleshooting | [docs/OPERATIONS.md](docs/OPERATIONS.md) |
| Backup / restore / DR | [docs/BACKUP_RESTORE.md](docs/BACKUP_RESTORE.md) |
| Threat model, Docker-socket risk, what is NOT protected | [docs/SECURITY.md](docs/SECURITY.md) |
| Tenant network/container isolation | [docs/TENANT_ISOLATION.md](docs/TENANT_ISOLATION.md) |
| Custom domains, DNS verification, TLS | [docs/DOMAINS.md](docs/DOMAINS.md) |
| Add-ons / managed databases | [docs/ADDONS.md](docs/ADDONS.md) |
| Every environment variable (generated from code) | [docs/ENVIRONMENT.md](docs/ENVIRONMENT.md) |
| Unit, CI and end-to-end testing | [docs/TESTING.md](docs/TESTING.md) |
| Local development, adding services/migrations/add-ons | [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) |
| Releases, images, SBOM, signing | [docs/RELEASE.md](docs/RELEASE.md) |
| Evidence-based feature matrix | [docs/FEATURE_STATUS.md](docs/FEATURE_STATUS.md) |
| API schema | [packages/openapi/openapi.yaml](packages/openapi/openapi.yaml), `jp.yaml` schema in [packages/jp-schema](packages/jp-schema) |
| Per-service notes | `services/<name>/README.md` |
