# JP Cloud Platform — Testing Strategy & Guidelines

This document details the automated testing strategy, execution runbooks, test hierarchy, and integration testing frameworks for the JP Open-Source Self-Hosted Cloud Platform.

---

## 1. Testing Philosophy & Hierarchy

The platform adheres to strict testing standards:
- **No Blind Mocks**: Business-critical paths (JWT verification, event envelopes, secret encryption, marketplace catalog, routing) must be verified with realistic assertions, not hollow mock expectations.
- **Layered Hierarchy**:
  1. **Unit Tests**: Blazing-fast tests for pure logic, token issuance, catalog lookups, data validation, and HTTP handlers via `net/http/httptest`.
  2. **Service Store Integration Tests**: Real database query tests running against isolated test schemas or ephemeral PostgreSQL containers.
  3. **Event Stream Integration Tests**: Publishing and consuming real envelopes via Redis Streams consumer groups (`jp-workers`, `jp-scheduler`).
  4. **Edge & SSL Tests**: Traefik integration tests using the local [Pebble ACME test server](https://github.com/letsencrypt/pebble) to verify automated certificate issuance and renewals without rate limits.

---

## 2. Running Automated Tests

### Windows Environment Note
On Windows systems where Application Control / AppLocker policies restrict binary execution from `AppData\Local\Temp`, set `GOTMPDIR` to a workspace directory before running tests:

```powershell
$env:GOTMPDIR = "c:\Users\Zeeshan\Music\cloud-plateform\.tmp"
New-Item -ItemType Directory -Force -Path $env:GOTMPDIR | Out-Null
```

### Run Unit Tests by Package
```powershell
# Common packages
go test -v ./packages/go-common/jwt/...
go test -v ./packages/go-common/httpx/...
go test -v ./packages/events/...

# Microservices
go test -v ./services/addon/internal/...
go test -v ./services/repository/internal/githubapp/...
```

### Run All Unit Tests
```powershell
go test -v `
  ./packages/go-common/jwt/... `
  ./packages/go-common/httpx/... `
  ./packages/events/... `
  ./services/addon/internal/... `
  ./services/repository/internal/githubapp/...
```

---

## 3. Package Testing Catalog

| Package / Module | Test File | Key Scenarios Covered |
| :--- | :--- | :--- |
| `packages/go-common/jwt` | `jwt_test.go` | Access/Refresh token minting, expiry validation, invalid signature rejection. |
| `packages/go-common/httpx` | `httpx_test.go` | JSON serialization, standard error contract formatting, body decoding, health check. |
| `packages/events` | `events_test.go` | Envelope constructor, UTC timestamps, payload unmarshaling, stream error handling. |
| `services/addon/catalog` | `catalog_test.go` | Default engine registry (Postgres, Valkey, Meilisearch, etc.), secret name generation, external JSON directory loading. |
| `services/addon/provisioner` | `provisioner_test.go` | Realistic simulated connection strings, dynamic fallback for custom marketplace apps. |
| `services/repository` | `client_test.go` | GitHub App authentication, private key parsing, installation token caching. |

---

## 4. Integration Testing with Real Dependencies

### Testing with PostgreSQL
Store integration tests use the `DATABASE_URL` environment variable. If set, migrations are automatically executed against a fresh test database:
```bash
export DATABASE_URL="postgres://jp:jp_dev_password@localhost:5432/jp_test?sslmode=disable"
go test -v ./services/addon/internal/store/...
```

### Testing ACME & Let's Encrypt with Pebble
To test automated SSL certificate provisioning without touching Let's Encrypt production rate limits:
1. Start the Pebble test container:
   ```yaml
   pebble:
     image: letsencrypt/pebble:latest
     ports: ["14000:14000", "15000:15000"]
     environment:
       - PEBBLE_VA_NOSLEEP=1
   ```
2. Configure Traefik's `caServer` to point to `https://localhost:14000/dir`.
3. Issue a domain verification request and assert certificate generation in `services/domain`.

---

## 5. CI/CD Pipeline Integration

The GitHub Actions workflow (`.github/workflows/ci.yml`) executes the test suite on every pull request:
1. Checks formatting (`gofmt -l .`).
2. Runs static analysis (`golangci-lint run`).
3. Starts ephemeral PostgreSQL and Redis service containers.
4. Executes unit and integration test suites with code coverage reporting (`-coverprofile=coverage.out`).
5. Enforces minimum 80% coverage on shared packages (`go-common`, `events`).
