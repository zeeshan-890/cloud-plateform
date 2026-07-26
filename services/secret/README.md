# secret service

Phase 5 — encrypted secrets + project environments.

**Port:** 8014

## Features

- AES-256-GCM envelope encryption (`SECRETS_MASTER_KEY`; falls back to a fixed dev key)
- Versioned secrets; API never returns plaintext after create/update (only `value_hint`)
- Environments: `development|preview|staging|production` (auto-provisioned per project)
- Audit trail in `secret_audit`
- Internal decrypt endpoint for runtime injection (not proxied by gateway)

## Env

| Variable | Default |
|----------|---------|
| `SECRETS_MASTER_KEY` | fixed local key (simulate-friendly) |
| `PORT` | 8014 |
