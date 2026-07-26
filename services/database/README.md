# database service

One-click managed Postgres for projects (schema-per-db on shared Postgres, or simulate).

**Port:** `8018`  
**Profile:** `data`

## Modes

| `DB_MODE` | Behavior |
|-----------|----------|
| `simulate` (default) | Records DB + connection hint; no real schema/role |
| `schema` | Creates isolated schema + role on shared Postgres; falls back to simulate if privileges fail |

Connection strings are stored via the secret service (`secret_ref`) when available.

## API

- `GET/POST /orgs/{orgId}/projects/{projectId}/databases`
- `GET/DELETE /orgs/{orgId}/projects/{projectId}/databases/{dbId}`

Via gateway: `/api/v1/...`
