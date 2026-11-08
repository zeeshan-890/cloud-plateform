# Domains and TLS

## Hostnames you get automatically

When a project is created the project service asks the domain service to register `<project-slug>-<org-slug>.<ROOT_DOMAIN>`
(removed again when the project is deleted). It is routed immediately and counts as verified because the platform owns `ROOT_DOMAIN`.
Previews use `<slug>.<PREVIEW_BASE_DOMAIN>` (default `preview.<ROOT_DOMAIN>`).

`ROOT_DOMAIN` is required (no built-in default). With the installer's `--ip-only` it is `<ip-with-dashes>.sslip.io`, a public
wildcard DNS service that resolves any `*.1-2-3-4.sslip.io` to `1.2.3.4`, so generated hostnames work without owning a domain.
For your own domain, create **one wildcard A record** `*.apps.example.com -> <server IP>` plus `panel.example.com -> <server IP>`.

## Attaching a custom domain (3 steps)

1. **Add** the domain to the project (Domains page / API). Hostnames are validated strictly; the response lists the exact DNS records to create.
2. **Create DNS records** at your DNS provider. Any one of these proves control, and the first two also route traffic:
   * `CNAME app.example.com -> <DOMAIN_CNAME_TARGET>` (shown in the UI; default `cname.<ROOT_DOMAIN>`), **or**
   * `A app.example.com -> <server public IP>` (`DOMAIN_A_TARGETS`, default `PUBLIC_IP`; needed for apex domains), **or**
   * `TXT _jp-challenge.app.example.com = jp-verify=<token>` (ownership only; token shown in the UI).
3. **Verify.** The domain service resolves the records itself (`DNS_RESOLVER` if set, else the system resolver) and marks the domain
   `verified`. There is no override flag. Only then is a Traefik router written and, with TLS, a certificate requested.

Unverified domains never get routers or certificates, so they cannot be used to hijack or probe other hosts.

## TLS

* `TLS_ENABLED=true` (installer with a domain): Traefik uses Let's Encrypt **HTTP-01** on port 80 per hostname, redirects http->https,
  stores state in the `traefik_acme` volume. Requires `ACME_EMAIL` and `MANAGEMENT_DOMAIN`; the host's DNS must already point at the server.
  `ACME_STAGING=true` uses the staging CA while testing. Wildcard certificates (DNS-01) are not supported; each hostname gets its own certificate (mind Let's Encrypt rate limits; sslip.io is on the public suffix list).
* `TLS_ENABLED=false`: plain http only, no redirect, no certificates. Domain statuses report `no TLS`.
* The certificate service reports issuance state by probing `TLS_PROBE_ADDR` (default `traefik:443`).

## How routing is generated

The domain service writes one file per host, `domain-<sha256(host)[:16]>.yml`, into the `traefik_dynamic` volume (atomic
tmp+rename) with `Host()` rule, `service: <current service>@docker` and entrypoints `web` (+ `websecure` and the cert resolver only
when TLS is on). Traefik watches the directory. For blue/green the runtime calls `PUT /internal/routes/{projectId}` to switch
the service atomically.

## Testing DNS flows without public DNS

`infra/compose/docker-compose.e2e.yml` adds a CoreDNS container the domain service resolves through (`DNS_RESOLVER=dns:53`).
Edit its zone with `scripts/e2e-dns.sh set _jp-challenge.app.e2e.test TXT jp-verify=<token>` - see [TESTING.md](TESTING.md).

## Troubleshooting

| Problem | Check |
|---|---|
| stays `pending` | `dig +short TXT _jp-challenge.<host>` / `dig +short CNAME <host>` from outside; the UI shows what was found |
| verified but 404 | domain attached to a project with a running deployment? `ls` the `traefik_dynamic` volume; `scripts/jp-compose.sh logs traefik` |
| no certificate | DNS must point to this server *before* the first request; port 80 reachable; `ACME_EMAIL` set; `logs traefik` shows ACME errors |
