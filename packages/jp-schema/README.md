# jp.yaml schema

JSON Schema (draft 2020-12) for the `jp.yaml` project manifest.

## Validate

- CLI: `jp deploy` / `jp config apply` validates against this schema before POSTing desired state.
- Project service: `PUT /orgs/{orgId}/projects/{projectId}/config` re-validates on apply.
- Dashboard: Config / drift stub reads last-applied config from the project service.

## Key fields

| Field | Notes |
|-------|--------|
| `name` | lowercase slug |
| `runtime` | `nodejs` \| `python` \| `go` \| `docker` \| `static` (aliases `node` / `node22`) |
| `deploy.strategy` | `rolling` (default) or `blue_green` |
| `deploy.replicas` | 1–10 |
| `domains` | hostnames for Traefik |
