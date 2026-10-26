# JP Cloud Platform — Local Development Guide

This guide describes how to set up, develop, debug, and run the JP Open-Source Self-Hosted Cloud Platform locally.

---

## 1. Prerequisites

Before starting, ensure your local development machine has:
- **Go**: `1.22.0` or higher (`go version`)
- **Node.js**: `v20.0.0` or higher (`node -v`)
- **Docker**: Engine `24.0+` with Docker Compose V2 (`docker compose version`)
- **Git**: For version control (`git --version`)
- **Make / PowerShell / Bash**: For executing build tasks

---

## 2. Monorepo Architecture & Go Workspace

The repository is structured as a Go Workspace (`go.work`) containing 25 independent modules:
- `packages/go-common`: Shared HTTP helpers, JWT managers, middleware, Postgres pools, Redis clients.
- `packages/events`: Redis Streams event bus definitions, envelopes, publisher/subscriber routines.
- `services/*`: 22 microservices, each with its own `go.mod` and isolated domain boundaries.
- `apps/cli`: The `jp` command-line interface.
- `apps/dashboard`: Next.js 16 / React 19 web management console.

### Key Rule for Go Workspace:
Global `go test ./...` from the workspace root will fail because the root directory has no `go.mod`.
Always target specific package directories or module paths:
```powershell
# Run tests for specific packages
go test -v ./packages/go-common/jwt/...
go test -v ./services/addon/internal/...

# Build specific service binaries
go build ./services/gateway/cmd/...
go build ./apps/cli/...
```

---

## 3. Local Development Setup

### Step 1: Clone and Configure Environment
```bash
git clone https://github.com/jp-cloud/cloud-platform.git
cd cloud-platform
cp .env.example .env
```

Ensure standard development ports are free on `localhost`:
- `5432`: PostgreSQL
- `6379`: Redis
- `80`: Traefik HTTP
- `443`: Traefik HTTPS
- `3000`: Dashboard Web Console
- `8000`: API Gateway

### Step 2: Start Core Infrastructure (Docker Compose)
Start the shared data plane dependencies (PostgreSQL, Redis, Traefik):
```bash
docker compose up -d postgres redis traefik
```

Verify containers are running:
```bash
docker compose ps
```

---

## 4. Running Backend Services Locally

You can run any microservice locally outside Docker while connecting to the Dockerized Postgres and Redis instances.

Example: Running the `addon` service:
```powershell
$env:DATABASE_URL="postgres://jp:jp_dev_password@localhost:5432/jp?sslmode=disable"
$env:REDIS_URL="redis://localhost:6379/0"
$env:JWT_SECRET="dev-super-secret-jwt-key-minimum-32-chars-long"
$env:HTTP_PORT="8021"
$env:ADDON_MODE="simulate"

go run ./services/addon/cmd/main.go
```

Example: Running the `gateway` service:
```powershell
$env:HTTP_PORT="8000"
$env:JWT_SECRET="dev-super-secret-jwt-key-minimum-32-chars-long"
$env:CORS_ORIGINS="http://localhost:3000"

go run ./services/gateway/cmd/main.go
```

---

## 5. Running the Web Console Dashboard

The dashboard is built with Next.js App Router and Tailwind CSS:

```bash
cd apps/dashboard
npm install
npm run dev
```

The web dashboard is now accessible at `http://localhost:3000`.

---

## 6. Using the CLI Locally

You can build and invoke the `jp` CLI directly from source:

```bash
# Build binary
go build -o jp.exe ./apps/cli

# Run commands
./jp.exe whoami
./jp.exe addon catalog --project <pid>
```

---

## 7. Database Migrations

Each microservice maintains forward-only SQL migration scripts inside `services/<name>/migrations/`:
- `001_init.sql`
- `002_add_index.sql`

On startup, each service calls `postgres.Migrate(ctx, pool, migDir)`, which creates the `schema_migrations` tracking table and runs unapplied migration files in lexical order inside a transaction.

---

## 8. Code Standards & Pull Request Checklist

1. **Format**: Format all Go code using standard `gofmt -s -w .`.
2. **Lint**: Run `golangci-lint run` or ensure zero compilation warnings.
3. **No Mocks in Core Logic**: Real behavior must be verified with integration tests or realistic in-memory test doubles.
4. **Tenant Scoping**: All SQL queries must enforce `WHERE org_id = $1` to prevent cross-tenant data leaks.
5. **Clickable Links**: In markdown documentation, use relative markdown links or standard URLs.
