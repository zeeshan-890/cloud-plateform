# domain service

Owns ALL Traefik routers (file provider, `TRAEFIK_DYNAMIC_DIR`). Config is always derived from the database:
reconciled on start-up and every 10 minutes, orphan `domain-*.yml` files are deleted.

* Custom domains are verified for real: TXT `jp-verify=<token>` at `_jp-challenge.<host>` (or on the host), a CNAME to
  `DOMAIN_CNAME_TARGET`, or A/AAAA equal to `DOMAIN_A_TARGETS`. There is no `force` and no stub mode.
  Verified domains are re-checked every 6 h and downgraded after two consecutive failures (router + certificate removed).
* Routers point at the project's current Traefik service (`jp-app-<short>@docker`, switched by `PUT /internal/routes/{projectId}`);
  previews use `jp-prev-<deploymentShort>@docker`. Unverified domains never get a router or a certificate.
* Multi-server: `PUT /internal/routes/{projectId}` body `{"service":"jp-app-<short>","upstreams":["http://10.0.0.179:32768"]}`.
  Upstreams (max 16, http only, literal IP + port, no loopback/link-local/multicast/unspecified/broadcast) render a
  `jp-u-<hash>@file` loadBalancer service in each domain file and are stored in `project_routes.upstreams`; omitting them clears them.
* One hostname can be routed by only ONE project (unique among verified/active); released on delete.
* System subdomain `<project-slug>-<org-slug>.<ROOT_DOMAIN>` is created by `POST /internal/domains/system`.
* RBAC (areas): list/get = `domains:view`, create/verify/delete = `domains:manage`.

| Env | Meaning |
|-----|---------|
| `ROOT_DOMAIN` | required in production |
| `TRAEFIK_DYNAMIC_DIR` | writable dir shared with Traefik (required in production) |
| `TRAEFIK_CERT_RESOLVER` | default `letsencrypt` |
| `TLS_ENABLED` | add `websecure` + certResolver to routers |
| `DOMAIN_CNAME_TARGET`, `DOMAIN_A_TARGETS` | edge name / public IPs customers point to |
| `DNS_RESOLVER` | `host:port` of the resolver used for verification (default system resolvers) |
| `MANAGEMENT_DOMAIN`, `PREVIEW_BASE_DOMAIN` | reserved names (cannot be claimed by tenants) |
| `PREVIEW_TLS` | TLS on preview routers (default false) |
| `DOMAIN_ALLOW_INTERNAL_TLDS` | allow `.local` etc. in production (default false) |

Internal API (internal token): `PUT /internal/routes/{projectId}`, `POST|DELETE /internal/domains/system[/{projectId}]`,
`POST /internal/domains/preview`, `DELETE /internal/domains/by-deployment/{id}`, `POST /internal/domains/{id}/reissue`,
`DELETE /internal/projects/{projectId}`.
