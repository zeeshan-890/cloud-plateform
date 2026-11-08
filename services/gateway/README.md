# gateway service

The only public API (via Traefik `/api`). Port 8000.

* Authenticates JWT / personal access tokens, applies rate limits, CORS and trusted-proxy client-IP handling, then proxies to the services
  with unbuffered streaming and per-route deadlines (long ones for add-on/database create, backup/restore and storage up/download).
* Returns `{"error":{"code","message"}}` with 502/504 when an upstream is down or slow; it never fabricates data.
* Own endpoints: `/healthz`, platform `/system/{settings,security,cluster}` (platform admin; real checks and single-node data), project listing filtered per user.
* Env: `JWT_SECRET`, `INTERNAL_TOKEN`, `REDIS_URL`, `TRUSTED_PROXIES`, `CORS_ORIGINS`, `PUBLIC_DASHBOARD_URL`, `TLS_ENABLED`, `INFRA_BIND_ADDR`, all `*_URL`.
* Count routes with `scripts/stats.sh`.
