# JP Cloud end-to-end suite

A black-box Go test suite (module `github.com/jp-cloud/e2e`, standard library only). It talks to a **deployed** platform over HTTP
(`/api/v1`, Traefik edge) and, for infrastructure assertions, over **SSH** (`docker inspect`, `docker network ls`, the Traefik dynamic
directory, `docker stats`). It never imports service code.

## Run

```bash
cd e2e
GOWORK=off E2E_BASE_URL=http://161.118.168.6 E2E_SSH=oracle-2nd go test -v -count=1 -timeout 90m ./...
# or
E2E_BASE_URL=http://161.118.168.6 E2E_SSH=oracle-2nd ./run.sh -run 'TestAuth|TestUsers'
```

Without `E2E_BASE_URL` every test is skipped. Without `E2E_SSH` the tests that need the host (containers, fixture image, docker
inspect, Traefik files, leak check) are skipped, the pure API tests still run.

## Environment

| Variable | Default | Meaning |
|---|---|---|
| `E2E_BASE_URL` | – (required) | Entry point (`http://HOST` via Traefik or `http://HOST:3000`). API is `<base>/api/v1`. |
| `E2E_SSH` | – | ssh alias of the server (key based, `BatchMode`). |
| `E2E_EDGE_HOST` / `E2E_EDGE_PORT` | host of base URL / `80` | Where Traefik answers for `Host:`-header routing tests. |
| `E2E_ADMIN_EMAIL` / `E2E_ADMIN_PASSWORD` | `admin@e2e.test` / random | Platform admin. If the platform is not yet initialised it is bootstrapped (`POST /auth/bootstrap`) with these; if it is, they are used to log in (**set both on a re-used server**). |
| `E2E_BOOTSTRAP_TOKEN` | – | Sent as `bootstrap_token` when the install requires one. |
| `E2E_ROOT_DOMAIN` | from the domain service config | Only used for assertions on system hostnames. |
| `E2E_IMAGE_REPO` | `localhost:5000/e2e/addon-probe` | Repo (no tag) of the fixture image built on the server (`:1`, `:2`, `:crash`). |
| `E2E_NO_PUSH` | – | Skip `docker push` of fixture images (images stay local to the server docker). |
| `E2E_REMOTE_REPO` | – | Path of the repo checkout **on the server** that contains `scripts/e2e-dns.sh`; required for the real DNS verification test (CoreDNS container from `docker-compose.e2e.yml`). |
| `E2E_DNS_ZONE` | `e2e.test` | Zone served by the CoreDNS test container. |
| `E2E_GIT_REPO` | `heroku/node-js-getting-started` | Public GitHub repo used by the git scenarios. `E2E_SKIP_GIT=1` skips the (slow) build scenarios. |
| `E2E_ALLOW_FAIL` | `tls` | Comma separated `/system/security` check ids that may be `fail` (TLS is off on the test host). |
| `E2E_TIMEOUT` | `90m` | Timeout used by `run.sh`. |

## Files

| File | Scenarios |
|---|---|
| `auth_test.go` | closed registration, bootstrap only once, refresh rotation + replay, logout revokes access and refresh, login rate limit, MFA enrol/login/replay/backup code, password change revokes all sessions, logout-all |
| `users_test.go` | admin-created users (generated password once), login by username, forced password change (limited token), `/auth/me` memberships, only granted project visible, 404/403 matrix, view-only cannot mutate, secret reveal flag, disable/reset kills sessions, cross-org IDOR, spoofed `X-Org-Role`/`X-User-ID` |
| `secrets_test.go` | create/list/reveal/rotate/rename/delete, no plaintext in lists, env injection into the running container, `preview` secrets never reach production |
| `deploy_test.go` | fixture image build on the server, hardening (`CapDrop ALL`, `no-new-privileges`, memory/pids limits, own `jp_p_<short>` network only, no docker.sock/binds/ports), edge routing, stop/start, **zero-downtime** rolling redeploy (100 ms poller), failed/crashing/missing image keeps the old version, rollback |
| `addons_test.go` | postgres/redis/mongodb/mysql/rabbitmq add-ons (only catalog-`provisionable` ones), readiness, env names, real write+read from inside the app container via `/probe`, no published ports / not reachable from outside, masked credentials for non-reveal users, 409 name/secret collision, delete removes container + secrets |
| `domains_test.go` | hostname injection/validation (newline, backtick, uppercase, IP, reserved, 300 chars) with nothing written to the Traefik dir, system subdomain routing, unverified domain = 404, **real DNS** TXT verification through CoreDNS, `force` ignored, wrong token, uniqueness across projects (409), record removal, delete removes router |
| `git_test.go` | dangerous clone URLs/branches rejected, public repo build + deploy, blue/green colour flip without downtime, rollback, webhook forged/unsigned/valid/replayed/ping, preview deployment gets no production secrets |
| `storage_test.go` | bucket, base64 + streaming upload, list, download, delete, traversal/reserved keys, 413 on oversize, cross-project isolation, RBAC |
| `database_test.go` | managed Postgres: create, app connects, backup, destructive change, restore verified (checked with `psql` inside the container), RBAC, delete |
| `cascade_test.go` | project delete removes containers, `jp_p_*` network, volumes, routers, secrets, add-ons; org delete cascades |
| `security_test.go` | infra ports closed from outside, tenant container cannot reach postgres/redis/secret/identity/gateway/runtime/docker socket/other tenants, `/internal/*` not exposed, 401 on every protected route, body size limits, `/system/security` + `/system/cluster` (admin only) |
| `leak_test.go` | memory/fd growth (< 30 % + 10 MiB, < 50 fds) of every `jp-*` container after 500 API calls + 10 project create/delete cycles; `RestartCount == 0`; nothing unhealthy |
| `harness.go`, `harness_ext.go`, `poll.go` | HTTP client (auto-refresh for the shared admin), ssh/docker helpers, fixture image build, uptime poller |
| `fixtures/addon-probe` | the Node app deployed as the "customer" program (`/health /env /probe /net /mem`) |

## Cleanup behaviour

Every resource is created with a unique `RandName(...)` (`e2e-xxxxxx`), so re-runs never collide. Each test registers `t.Cleanup` handlers
that delete what it created, in reverse order: users, projects (the project cascade removes containers, networks, volumes, routers,
secrets and add-ons), then organizations. If a project delete fails, the cleanup additionally force-removes containers labelled
`jp.project=<id>` over ssh. Fixture images and the temporary build directory (`/tmp/jp-e2e-*`) stay/are removed respectively; DNS test
records are deleted. The shared platform admin is created once (idempotent) and kept.

## Notes for the operator

* Tests run sequentially (they share one admin and measure uptime / resource usage). Expect 40-70 minutes with the git builds.
* Rate-limit tests lock **throwaway** users only; they use 12-14 failed logins which stays under the per-IP limit (40 / 15 min).
* The DNS verification test needs the CoreDNS container: `scripts/jp-up.sh --e2e` and `E2E_REMOTE_REPO`.
* Known black-box gaps are reported as `t.Logf("GAP: ...")` or failing assertions that name the missing route (see the final report of the suite author).
