# go-common

Shared Go libraries for jp microservices:

- `config` — env-based configuration
- `postgres` — pgx pool + SQL file migrations
- `redis` — Redis client helper
- `jwt` — access/refresh JWT manager
- `httpx` — JSON responses & error contract
- `middleware` — request ID, CORS, logging, bearer auth
- `logging` — structured `slog` JSON logger
- `audit` — audit log writer helpers
- `otelx` — request/trace ID propagation (`X-Request-ID`, `X-Trace-ID`, optional `traceparent`); set `OTEL_EXPORTER_OTLP_ENDPOINT` for Tempo
