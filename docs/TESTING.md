# Testing

## Unit and package tests

Every service is its own Go module listed in `go.work`. **`go test ./services/...` from the repo root does not work** with a
workspace of many modules; run per module (this is exactly what CI does):

```bash
scripts/test-all.sh            # go vet + go test -race for every module in go.work   (Windows: scripts\test-all.ps1)
scripts/test-all.sh --short    # without -race (no CGO needed)
cd services/domain && go test ./...        # one module
```

`-race` needs CGO (gcc). Tests use `httptest` and fake upstreams; they need no Docker, Postgres or Redis.

Dashboard: `cd apps/dashboard && npm ci && npm run lint && npx tsc --noEmit && npm run build`.

## Static checks (CI: `.github/workflows/ci.yml`)

| Job | Command |
|---|---|
| gofmt | `gofmt -l apps packages services e2e` must print nothing |
| vet + race tests | loop above |
| standalone build | each service builds with `GOWORK=off` (as in the Dockerfile) |
| govulncheck | every module, Go `stable` toolchain |
| compose | `docker compose config` for prod, dev, e2e, sizing tiers; fails if prod has insecure defaults or a datastore bound publicly |
| env contract | `scripts/verify-env.sh` - every env var read by Go code is passed to that service in compose and documented in `.env.example` |
| shell / PowerShell | `shellcheck`, `bash -n`, PowerShell parser |
| known hosts | `infra/docker/github_known_hosts` equals `https://api.github.com/meta` |
| images | Dockerfiles build (amd64); release builds amd64+arm64 |

## End-to-end tests

`e2e/` is a separate Go module (not in `go.work`) that drives a **deployed** platform over HTTP and, optionally, SSH:

```bash
cd e2e
GOWORK=off E2E_BASE_URL=http://SERVER:3000 E2E_SSH=oracle-2nd go test -v -count=1 -timeout 60m ./...
```

Variables: `E2E_BASE_URL` (required, else tests skip), `E2E_SSH`, `E2E_EDGE_HOST`/`E2E_EDGE_PORT` (Traefik, to test `Host:` routing),
`E2E_ROOT_DOMAIN`, `E2E_ADMIN_EMAIL`, `E2E_ADMIN_PASSWORD`. The target must be a fresh install (the suite bootstraps the first admin)
and should be started with the e2e override so custom-domain verification uses a DNS server the suite controls:

```bash
scripts/jp-up.sh --e2e                       # adds the CoreDNS test server, DNS_RESOLVER=dns:53 on the domain service
scripts/e2e-dns.sh set _jp-challenge.shop.e2e.test TXT jp-verify=<token>
scripts/e2e-dns.sh del _jp-challenge.shop.e2e.test
scripts/e2e-dns.sh show
dig -p 5353 @127.0.0.1 TXT _jp-challenge.shop.e2e.test       # what the domain service will see
```

Zone files live in `infra/e2e/dns/zones/db.<zone>`; CoreDNS rescans every 2 seconds and each edit bumps the SOA serial.
The suite builds public GitHub repositories (no git server is run). Fixture apps are under `e2e/fixtures`.

## Local stack smoke test

```bash
scripts/jp-up.sh --dev --wait       # dev defaults from infra/compose/dev.env
curl -fsS http://127.0.0.1:8000/healthz
```
