# logging service

Phase 5 — log ingest/query for build + runtime.

**Port:** 8015

Stores entries in Postgres by default. When `LOKI_URL` is set (monitoring profile), also pushes to Loki.

Can merge build worker logs from the build service when querying `source=build&build_id=...`.
