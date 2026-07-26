# storage service

Object storage for org/project buckets (MinIO or simulate).

**Port:** `8017`  
**Profile:** `data` (with MinIO)

## Modes

| `STORAGE_MODE` | Behavior |
|----------------|----------|
| `simulate` (default) | In-memory objects + Postgres metadata; fake signed URLs |
| `minio` | Real MinIO; falls back to simulate if MinIO unreachable |

## API

- `GET/POST /orgs/{orgId}/projects/{projectId}/storage/bucket`
- `GET /orgs/{orgId}/projects/{projectId}/storage/objects?prefix=`
- `POST /orgs/{orgId}/projects/{projectId}/storage/objects` — `{ key, data_base64, content_type? }`
- `GET .../storage/objects/{key}/signed-url?expires=15m`
- `DELETE .../storage/objects/{key}`

Via gateway: `/api/v1/...`
