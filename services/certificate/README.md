# certificate service

Tracks TLS certificate status and renew metadata. Integrates with Traefik ACME via resolver name / router labels recorded in `metadata`.

| Env | Default |
|-----|---------|
| `PORT` | `8013` |
| `CERT_SIMULATE` | `true` — issue/renew locally without Let's Encrypt |
| `TRAEFIK_CERT_RESOLVER` | empty — set e.g. `letsencrypt` when Traefik ACME is configured |

Public: list/get/renew certificates. Internal: `POST /internal/certificates` (from domain service).
