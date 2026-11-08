# Notes for coding agents and contributors

Short, factual orientation. Authoritative docs live in `docs/`; do not copy numbers from here - recompute them (`scripts/stats.sh`).

## Shape of the repo

* Go workspace (`go.work`): 21 service modules under `services/`, `packages/go-common`, `packages/events`, `apps/cli`.
  `e2e/` is a separate module (`GOWORK=off`). Dashboard: `apps/dashboard` (Next.js static export).
* Deployment: `infra/compose/docker-compose.yml` (production, no default secrets), `docker-compose.dev.yml` + `dev.env` (local),
  `docker-compose.e2e.yml` (test DNS), `sizing/*.yml`. Images: `infra/docker/Dockerfile.service` (all services),
  `Dockerfile.worker` (git, docker CLI, buildx, trivy).
* Operations: `install.sh`, `scripts/{jp-up,jp-compose,backup,restore,upgrade,size-host,verify-env,gen-env-docs,test-all,e2e-dns}.sh`.

## Rules that must keep holding

1. Env contract: anything a service reads with `os.Getenv`/`getEnv`/`GetEnvBool` must be set in that service's compose entry and
   documented in `.env.example`; run `scripts/verify-env.sh`. Regenerate `docs/ENVIRONMENT.md` with `scripts/gen-env-docs.sh`.
2. No default secrets in production compose. Required values use `${VAR:?message}`; dev values live only in `infra/compose/dev.env`.
3. Tenant containers never join the `jp` network, never get the Docker socket, host mounts, privileges or router labels.
4. All `/internal/*` routes use `internalauth.Require`; all service-to-service calls use `internalauth.NewClient`.
5. Project-scoped handlers use `membership.RequireProject` (fail closed) with the role from the RBAC policy.
6. Errors are `{"error":{"code","message"}}`; the gateway returns 502 in that shape when an upstream is down - it never invents data.
7. Simulation modes exist only for tests/dev, are never the default, and must be visible in responses.
8. Every claim in README/docs must be true for the code: update `docs/FEATURE_STATUS.md` (with the verifying test) when behaviour changes.

## Commands

```bash
scripts/test-all.sh                 # vet + race tests for every module
scripts/verify-env.sh               # env contract
docker compose -f infra/compose/docker-compose.yml --env-file infra/compose/dev.env config --quiet
cd apps/dashboard && npm ci && npm run lint && npx tsc --noEmit && npm run build
```

Do not run `go test ./services/...` from the root (does not work with a multi-module workspace). Do not commit `.env`, `.env.resources`, `backups/`.
