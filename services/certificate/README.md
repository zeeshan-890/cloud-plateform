# certificate service

Certificate status is derived from reality: a TLS handshake (SNI = hostname) against Traefik (`TLS_PROBE_ADDR`, default
`traefik:443`) reads the served certificate (issuer, notAfter, SANs) and verifies the chain against the system roots.
States: `pending` (Traefik still serves its default cert), `issued`, `expiring` (< 21 days), `failed` (error text),
`tls_disabled` (when `TLS_ENABLED=false`). A periodic scan (2 min; issued certs hourly) updates the DB and publishes events.

`POST .../certificates/{id}/renew` re-probes; if there is no valid certificate (or it is expiring) it asks the domain service
to re-write the router so Traefik retries ACME. Valid certificates are renewed by Traefik itself.

| Env | Meaning |
|-----|---------|
| `TLS_ENABLED` | `false` = report `tls_disabled` |
| `TLS_PROBE_ADDR` | Traefik websecure address |
| `ACME_STAGING` | accept Let's Encrypt staging chains |
| `TRAEFIK_CERT_RESOLVER` | recorded resolver name |

RBAC (areas): list/get = `domains:view`, renew = `domains:manage`. Internal: `POST /internal/certificates`, `POST /internal/certificates/scan`,
`DELETE /internal/certificates/by-domain/{id}`, `GET|DELETE /internal/projects/{id}[/certificates]`.
