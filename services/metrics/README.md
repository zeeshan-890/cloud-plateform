# metrics service

Phase 5 — thin metrics API + Prometheus scrape endpoint.

**Port:** 8016

- `GET /metrics` — Prometheus text exposition for this service
- Project summary API (simulate seeds sample series when `METRICS_MODE=simulate`)
- Scrape target annotations for Prometheus HTTP SD / file_sd

Prometheus itself runs under Compose `--profile monitoring`.
