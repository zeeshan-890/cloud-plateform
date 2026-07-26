# build

Phase 3 — Build jobs API (port **8007**).

- Internal `POST /internal/builds` enqueues to Redis Stream `jp.build`
- List/get builds; `GET .../builds/{id}/logs`
