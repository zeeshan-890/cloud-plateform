# metrics service

Port **8016**

- Project metric samples: `POST/GET /orgs/{o}/projects/{p}/metrics` (member+ to write, viewer+ to read, project-bound).
- `POST /internal/metrics` lets platform services (runtime) push container samples; `DELETE /internal/projects/{id}` is part of the project delete cascade.
- Samples older than `METRICS_RETENTION_DAYS` (default 14) are deleted hourly.
- `GET /metrics` exposes only service-level counters (no tenant labels).
- `METRICS_MODE=simulate` (dev/test only, refused in production) seeds fake series and reports `"mode":"simulate"`; the default is `live`.
- Removed: scrape targets/Prometheus SD (nothing ever scraped them) and the unused alert-rule engine.
