<p align="center">
  <img src="https://img.shields.io/badge/Go-1.22-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="Go"/>
  <img src="https://img.shields.io/badge/Next.js-16-000000?style=for-the-badge&logo=nextdotjs&logoColor=white" alt="Next.js"/>
  <img src="https://img.shields.io/badge/React-19-61DAFB?style=for-the-badge&logo=react&logoColor=black" alt="React"/>
  <img src="https://img.shields.io/badge/TypeScript-5-3178C6?style=for-the-badge&logo=typescript&logoColor=white" alt="TypeScript"/>
  <img src="https://img.shields.io/badge/PostgreSQL-16-4169E1?style=for-the-badge&logo=postgresql&logoColor=white" alt="PostgreSQL"/>
  <img src="https://img.shields.io/badge/Redis-7-DC382D?style=for-the-badge&logo=redis&logoColor=white" alt="Redis"/>
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?style=for-the-badge&logo=docker&logoColor=white" alt="Docker"/>
  <img src="https://img.shields.io/badge/Traefik-v3-24A1C1?style=for-the-badge&logo=traefikproxy&logoColor=white" alt="Traefik"/>
  <img src="https://img.shields.io/badge/License-MIT-green?style=for-the-badge" alt="License"/>
</p>

# ☁️ JP Cloud Platform

**A production-grade, open-source, self-hosted Platform-as-a-Service (PaaS) built entirely from scratch.**

Deploy, build, and manage applications on your own infrastructure with the developer experience of Vercel, Railway, and Coolify — but fully self-hosted and customizable.

> **42,000+ lines of code · 23 Go microservices · Next.js 16 dashboard · 25-command CLI · 241+ one-click marketplace apps**

---

### ⚡ 1-Line Automatic Complete Install

Run this single command on any clean Linux or macOS server (Ubuntu, Debian, Oracle Linux, CentOS, Rocky Linux, AlmaLinux) to automatically install Docker, set up dependencies, configure the firewall, generate secure secrets, and launch the full platform:

```bash
curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/scripts/setup-server.sh | bash
```

> **What this does automatically:**
> - 🐳 **Prerequisite Setup**: Installs Docker Engine, Docker Compose V2, and Git if not present
> - 🛡️ **Firewall Rules**: Automatically opens ports `80`, `443`, and `3000`
> - 🔐 **Security & Secrets**: Generates cryptographically secure tokens (`JWT_SECRET`, `SECRETS_MASTER_KEY`, DB passwords)
> - 🧠 **Hardware Sizing**: Auto-detects server RAM & CPU to apply optimal memory limits and worker counts
> - 🚀 **Zero-Config Launch**: Builds and launches the Traefik v3 ingress, API Gateway, core microservices, and Next.js 16 dashboard
> - 🌐 **Instant Readiness**: Ready in under 2 minutes at `http://<your-server-ip>:3000`

<details>
<summary><b>💻 Local Development & Windows PowerShell Commands (click to expand)</b></summary>

**Linux / macOS (local clone):**
```bash
git clone https://github.com/zeeshan-890/cloud-plateform.git && cd cloud-plateform && ./install.sh
```

**Windows (PowerShell):**
```powershell
git clone https://github.com/zeeshan-890/cloud-plateform.git; cd cloud-plateform; .\install.ps1
```
</details>

---

## 🌟 Features at a Glance

| Category | Highlights |
|---|---|
| **🔐 Auth & Identity** | User registration, bcrypt hashing, dual JWT lifecycle, session management, PATs (`jp_pat_*`), TOTP MFA, Redis rate limiting |
| **👥 Multi-Tenancy** | Organizations, team RBAC (owner/admin/member/viewer), email invitations with cryptographic tokens |
| **🚀 Git-to-Deploy** | GitHub App integration, push-to-deploy, PR preview environments, HMAC-SHA256 webhook verification |
| **🏗️ Build Farm** | Docker BuildKit workers, Redis Streams queue, horizontal scaling (1–8 workers), OCI registry, build log streaming |
| **📦 Container Runtime** | Docker Engine orchestration, rolling & blue-green deployments, instant rollbacks, scheduled cleanup |
| **🌐 Domains & TLS** | Custom domain binding, DNS verification, dynamic Traefik ingress, automated Let's Encrypt ACME certificates |
| **🔒 Secrets** | AES-256-GCM envelope encryption, multi-environment stages, version history, audit trail, zero-plaintext API |
| **📊 Observability** | Prometheus, Grafana, Loki, Tempo, OpenTelemetry tracing, centralized logging, resource metrics & alerts |
| **💾 Storage & DB** | MinIO S3 object storage, managed PostgreSQL schemas, presigned URLs, backup/restore engine |
| **🛒 Marketplace** | 241+ one-click add-on engines (databases, caches, search, queues, analytics, ML, etc.), zero-recompile JSON plugins |
| **🤖 AI Ops** | OpenAI-powered incident diagnosis, build/runtime failure analysis, rule-based heuristic fallback |
| **📄 Infrastructure as Code** | `jp.yaml` manifest with JSON Schema validation, SHA-256 drift detection, declarative desired state |
| **🖥️ Multi-Server** | Node agent daemon, capacity-aware placement, heartbeat monitoring, cluster topology dashboard |
| **💳 Billing** | Usage metering, plan management, build-minutes & runtime-hours tracking |

---

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        EDGE INGRESS                                 │
│               Traefik v3  (:80 / :443 / :8080)                     │
│         TLS Termination · ACME · Dynamic File Provider              │
├─────────────────────────────────────────────────────────────────────┤
│                        API GATEWAY (:8000)                          │
│     JWT Auth · PAT→JWT Exchange · RBAC · CORS · W3C Tracing        │
├─────────────────────────────────────────────────────────────────────┤
│                        CONTROL PLANE                                │
│                                                                     │
│  identity (8001)      organization (8002)   project (8003)         │
│  notification (8004)  repository (8005)     deployment (8006)      │
│  build (8007)         registry-api (8009)   domain (8012)          │
│  certificate (8013)   secret (8014)         logging (8015)         │
│  metrics (8016)       storage (8017)        database (8018)        │
│  ai (8019)            billing (8020)        addon (8021)           │
├─────────────────────────────────────────────────────────────────────┤
│                        DATA PLANE                                   │
│                                                                     │
│  worker ×N (8008)     registry:2 (5000)     scheduler (8011)       │
│  runtime (8010)       node-agent (daemon)                           │
├─────────────────────────────────────────────────────────────────────┤
│                        PERSISTENCE                                  │
│                                                                     │
│  PostgreSQL 16        Redis 7 Streams       MinIO S3               │
├─────────────────────────────────────────────────────────────────────┤
│                        OBSERVABILITY                                │
│                                                                     │
│  Prometheus (9090)  Grafana (3001)  Loki (3100)  Tempo (3200)      │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 📋 Table of Contents

- [⚡ 1-Line Automatic Install](#-1-line-automatic-complete-install)
- [Quick Start](#-quick-start)
- [Installation](#-installation)
- [Dashboard](#-dashboard)
- [CLI Reference](#-cli-reference-jp)
- [Compose Profiles](#-compose-profiles)
- [API Documentation](#-api-documentation)
- [Configuration](#%EF%B8%8F-configuration)
- [Adaptive Resource Sizing](#-adaptive-resource-sizing)
- [Technology Stack](#-technology-stack)
- [Project Structure](#-project-structure)
- [Real vs Simulated Modes](#-real-vs-simulated-modes)
- [Security](#-security)
- [Development](#-development)
- [Documentation](#-documentation)
- [Roadmap](#-roadmap)
- [Contributing](#-contributing)
- [License](#-license)

---

## 🚀 Quick Start

### Prerequisites

- [Docker Engine](https://docs.docker.com/engine/install/) 24.0+ with [Docker Compose V2](https://docs.docker.com/compose/)
- [Go 1.22+](https://go.dev/dl/) (for CLI / service development)
- [Node.js 20+](https://nodejs.org/) (for dashboard development)

### One-Command Install (Linux/macOS)

```bash
git clone https://github.com/zeeshan-890/cloud-plateform.git
cd cloud-plateform
./install.sh
```

### Step-by-Step Setup

**1. Clone & configure environment:**

```bash
git clone https://github.com/zeeshan-890/cloud-plateform.git
cd cloud-plateform
cp .env.example .env        # Edit .env with your settings
```

**2. Detect host resources & start:**

```bash
# Linux / macOS
./scripts/size-host.sh                    # Auto-detect host RAM/CPU → .env.resources
./scripts/jp-up.sh platform,ui            # Start with platform + dashboard

# Windows (PowerShell)
.\scripts\size-host.ps1
.\scripts\jp-up.ps1 -Profiles platform,ui
```

**3. Access the platform:**

| Service | URL |
|---|---|
| 🌐 **Dashboard** | http://localhost:3000 |
| 🔌 **API Gateway** | http://localhost:8000/api/v1 |
| 💚 **Health Check** | http://localhost:8000/healthz |
| ⚙️ **Traefik Dashboard** | http://localhost:8080 |

**4. Create your first account:**

Open http://localhost:3000 and register your admin account. You're ready to go!

---

## 📦 Installation

### Automated Installer

The canonical installer handles everything — prerequisite checks, secret generation, host sizing, and service startup:

```bash
# Linux/macOS
./install.sh [lite|small|medium|large|xlarge|auto]

# Windows PowerShell
.\install.ps1
```

The installer will:
1. ✅ Validate Docker Engine and Compose V2 are installed
2. ✅ Check minimum 5GB free disk space
3. ✅ Auto-detect host capacity (RAM/CPU)
4. ✅ Generate cryptographically random secrets (`JWT_SECRET`, `SECRETS_MASTER_KEY`, DB password)
5. ✅ Apply appropriate sizing overlay
6. ✅ Start all services with Docker Compose
7. ✅ Wait for gateway health check and report status

### Manual Docker Compose

```bash
# Core services (auth, orgs, projects, gateway)
docker compose -f infra/compose/docker-compose.yml --env-file .env up -d --build

# Full platform with build workers
docker compose -f infra/compose/docker-compose.yml --env-file .env \
  --profile platform --profile ui up -d --build

# With data services (storage, databases, addons)
docker compose -f infra/compose/docker-compose.yml --env-file .env \
  --profile platform --profile data --profile ui up -d --build

# With monitoring stack
docker compose -f infra/compose/docker-compose.yml --env-file .env \
  --profile monitoring up -d
```

### Uninstall

```bash
./uninstall.sh               # Preserves data volumes
./uninstall.sh --purge-data  # Full teardown including volumes
```

---

## 🖥️ Dashboard

The web dashboard is a full-featured admin console built with **Next.js 16**, **React 19**, and **TypeScript**.

### Starting the Dashboard

```bash
# Via Docker (recommended)
docker compose -f infra/compose/docker-compose.yml --env-file .env --profile ui up -d --build

# Local development
cd apps/dashboard
npm install
npm run dev          # → http://localhost:3000
```

### Dashboard Pages (31 Routes)

| Section | Pages | Description |
|---|---|---|
| **Home** | Landing, Login, Register | Authentication flow with JWT |
| **Organizations** | Org list, Team management, Invitations | Multi-tenant workspace management |
| **Projects** | Project list, App overview | Project CRUD and overview |
| **Source Control** | Git connections, Deploy keys | GitHub App integration, SSH keys |
| **Deployments** | Deployment history, Builds, Build logs | CI/CD pipeline monitoring |
| **Runtime** | Container instances, Start/Stop | Live container management |
| **Domains** | Domain list, DNS verification | Custom domain binding & TLS |
| **Secrets** | Secret management per environment | Encrypted secrets with version history |
| **Storage** | Object browser, Upload/Download | MinIO S3 file management |
| **Databases** | Managed DB provisioning | One-click PostgreSQL schemas |
| **Marketplace** | 241+ add-on engines | One-click service deployment |
| **Observability** | Logs, Metrics | Centralized logging & resource metrics |
| **AI Ops** | AI diagnosis & Q&A | Build/runtime failure analysis |
| **Billing** | Usage & Plans | Resource metering |
| **Cluster** | Node topology, Health monitoring | Multi-server management |
| **Security** | Security center, Posture score | Vulnerability scanning & hardening |
| **Settings** | Platform config, Sessions, Keys | System-wide configuration |

---

## ⌨️ CLI Reference (`jp`)

### Building the CLI

```bash
cd apps/cli
go build -o jp .          # Linux/macOS
go build -o jp.exe .      # Windows
```

### Commands

```bash
# Authentication
jp login                                          # Interactive login
jp logout                                         # Clear credentials
jp whoami                                         # Current user info

# Organizations
jp orgs                                           # List organizations
jp org use <slug|id>                              # Switch active org

# Projects
jp projects                                       # List projects
jp init                                           # Scaffold new project (Node.js/Go)

# Deployments
jp deploy --project <id>                          # Trigger deployment
jp apply --project <id>                           # Validate + apply jp.yaml

# Builds
jp builds --project <id>                          # List builds

# Domains
jp domains list --project <id>                    # List domains
jp domains add app.example.com --project <id>     # Add custom domain
jp domains verify <domain-id> --project <id>      # Verify DNS

# Secrets & Environment Variables
jp secrets list --project <id> --env production   # List secrets
jp secrets set API_KEY value --project <id>       # Set a secret
jp env list --project <id>                        # List env vars
jp env set DATABASE_URL <url> --project <id>      # Set env var

# Observability
jp logs --project <id> --source runtime           # View logs
jp metrics --project <id>                         # View metrics

# Storage
jp storage bucket --project <id>                  # Ensure bucket exists
jp storage put file.txt "content" --project <id>  # Upload object
jp storage ls --project <id>                      # List objects

# Databases
jp db create mydb --project <id>                  # Create managed DB
jp db list --project <id>                         # List databases

# Add-ons
jp addon catalog --project <id>                   # Browse marketplace
jp addon create redis --engine redis --project <id>  # Provision add-on

# AI Operations
jp ai "why did my build fail?" --project <id>     # AI diagnosis

# Configuration
jp config --project <id>                          # View jp.yaml config
```

---

## 🐳 Compose Profiles

| Profile | Services Started | Use Case |
|---|---|---|
| *(default/core)* | Traefik, Gateway, Identity, Organization, Project, Notification, PostgreSQL, Redis | Minimum viable platform |
| `platform` | Repository, Deployment, Build, Worker, Registry, Runtime, Scheduler, Domain, Certificate, Secret, Logging, Metrics, AI, Billing | Full CI/CD + operations |
| `ui` | Dashboard (Next.js on `:3000`) | Web console |
| `data` | MinIO, Storage, Database, Addon | Object storage + managed DBs + marketplace |
| `monitoring` | Prometheus, Grafana, Loki, Tempo, Promtail | Full observability stack |
| `dev` | MailHog | Email testing |
| `addons` | MySQL, MongoDB, Redis, RabbitMQ, Redpanda | Shared backing services for add-ons |

### Common Profile Combinations

```bash
# Development (everything)
./scripts/jp-up.sh platform,ui,data,monitoring

# Production (no monitoring to save resources)
./scripts/jp-up.sh platform,ui,data

# Minimal demo
./scripts/jp-up.sh ui
```

---

## 📡 API Documentation

**Base URL:** `http://localhost:8000/api/v1`
**Authentication:** `Authorization: Bearer <access_token>`
**Full OpenAPI Spec:** [`packages/openapi/openapi.yaml`](packages/openapi/openapi.yaml)

### Core Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/auth/register` | Create new user account |
| `POST` | `/auth/login` | Login and receive JWT tokens |
| `POST` | `/auth/refresh` | Refresh access token |
| `GET` | `/me` | Current user profile |
| `GET/POST` | `/orgs` | List / create organizations |
| `GET/POST` | `/orgs/{orgId}/projects` | List / create projects |

### Deployment & Build Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/orgs/{orgId}/projects/{projectId}/deployments` | Trigger deployment |
| `GET` | `/orgs/{orgId}/projects/{projectId}/deployments` | List deployments |
| `POST` | `.../deployments/rollback` | Rollback to previous |
| `GET` | `.../builds` | List builds |
| `GET` | `.../builds/{id}/logs` | Stream build logs |

### Infrastructure Endpoints

| Method | Endpoint | Description |
|---|---|---|
| `GET/POST/DELETE` | `.../domains` | Manage custom domains |
| `POST` | `.../domains/{id}/verify` | Verify DNS |
| `GET/POST/PUT/DELETE` | `.../environments/{env}/secrets` | Manage secrets |
| `GET/POST` | `.../storage/objects` | Manage files in S3 |
| `GET/POST/DELETE` | `.../databases` | Manage PostgreSQL schemas |
| `POST` | `.../ai/explain` | AI incident diagnosis |
| `POST` | `/orgs/{orgId}/ai/ask` | Free-form AI Q&A |

---

## ⚙️ Configuration

All configuration is done via environment variables in `.env`. Copy from the example:

```bash
cp .env.example .env
```

### Key Variables

| Variable | Default | Description |
|---|---|---|
| `JP_ENV` | `development` | Environment mode |
| `JWT_SECRET` | *(generate)* | HMAC-SHA256 signing key |
| `SECRETS_MASTER_KEY` | *(generate)* | 32-byte AES-256 encryption key |
| `DATABASE_URL` | `postgres://jp:...@postgres:5432/jp` | PostgreSQL connection |
| `REDIS_URL` | `redis://redis:6379/0` | Redis connection |
| `BUILD_MODE` | `docker` | `docker` or `simulate` |
| `RUNTIME_MODE` | `docker` | `docker` or `simulate` |
| `STORAGE_MODE` | `minio` | `minio` or `simulate` |
| `GITHUB_APP_ID` | *(optional)* | GitHub App for push-to-deploy |
| `OPENAI_API_KEY` | *(optional)* | OpenAI for AI-powered diagnostics |
| `CORS_ORIGINS` | `localhost:3000,5173` | Allowed CORS origins |

---

## 📐 Adaptive Resource Sizing

JP auto-detects your host's RAM and CPU and scales accordingly:

| Tier | Host RAM | Workers | Concurrent Builds | Ideal For |
|---|---|---|---|---|
| **Lite** | 1–2 GB | Remote | External | $3–$5/mo VPS, staging |
| **Small** | < 6 GB | 1 | 1 | Single-developer servers |
| **Medium** | 6–16 GB | 2 | 2 | Team production workloads |
| **Large** | 16–32 GB | 4 | 2 | Multi-project production |
| **XLarge** | 32+ GB | 8 | 4 | High-traffic infrastructure |

```bash
# Auto-detect (recommended)
./scripts/size-host.sh

# Force a specific tier
./scripts/jp-up.sh platform,ui --size large
```

Sizing overlays are located in `infra/compose/sizing/{lite,small,medium,large,xlarge}.yml` and adjust PostgreSQL memory, Redis maxmemory, worker concurrency, and container CPU limits.

---

## 🔧 Technology Stack

### Backend

| Technology | Version | Purpose |
|---|---|---|
| Go | 1.22 | All 23 microservices, CLI, shared packages |
| PostgreSQL | 16 | Primary relational database |
| Redis | 7 | Event streaming (Streams), caching, rate limiting |
| MinIO | Latest | S3-compatible object storage |
| Docker Engine | 24+ | Container runtime & build execution |
| Traefik | v3 | Edge proxy, TLS termination, dynamic routing |

### Frontend

| Technology | Version | Purpose |
|---|---|---|
| Next.js | 16 | Dashboard web application (SSG) |
| React | 19 | UI framework |
| TypeScript | 5 | Type-safe development |
| Tailwind CSS | 4 | Utility-first styling |

### DevOps & Infrastructure

| Technology | Purpose |
|---|---|
| Docker Compose | Multi-service orchestration (6 profiles) |
| GitHub Actions | CI (tests, build, compose validation) + CD (multi-arch release) |
| Docker Buildx | Multi-architecture images (amd64/arm64) |
| GHCR | Container image distribution |
| Anchore SBOM | Software Bill of Materials (SPDX) |
| Prometheus | Metrics collection |
| Grafana | Dashboards & visualization |
| Loki | Log aggregation |
| Tempo | Distributed tracing |
| OpenTelemetry | W3C trace context propagation |

### Security

| Feature | Implementation |
|---|---|
| Password hashing | bcrypt (cost=10) |
| JWT tokens | HMAC-SHA256 (HS256), dual access/refresh |
| Secret encryption | AES-256-GCM, 12-byte random nonces |
| PAT storage | SHA-256 hashed at rest |
| MFA | TOTP (RFC 6238) with backup codes |
| Rate limiting | Redis sliding window (5 attempts/15min) |
| Webhook auth | HMAC-SHA256 signature verification |
| Container isolation | Non-root execution, private networks |

---

## 📁 Project Structure

```
cloud-plateform/
├── apps/
│   ├── cli/                    # jp CLI tool (Go + Cobra, 25 commands)
│   │   ├── cmd/                # Command implementations
│   │   ├── internal/           # CLI helpers & HTTP client
│   │   └── main.go             # Entry point
│   └── dashboard/              # Next.js 16 web console
│       ├── src/app/            # App router (31 routes)
│       ├── src/components/     # React components
│       └── src/lib/            # API client, auth, types
│
├── services/                   # 23 Go microservices
│   ├── gateway/                # API facade & reverse proxy
│   ├── identity/               # Auth, users, sessions, MFA, PATs
│   ├── organization/           # Multi-tenant orgs, RBAC
│   ├── project/                # Projects, jp.yaml, drift detection
│   ├── notification/           # Events & email dispatch
│   ├── repository/             # GitHub integration, webhooks
│   ├── deployment/             # Deploy state machine, rollbacks
│   ├── build/                  # Build records & log streaming
│   ├── worker/                 # Docker BuildKit executor
│   ├── registry/               # OCI image catalog
│   ├── runtime/                # Container lifecycle (Docker socket)
│   ├── scheduler/              # Rolling updates, GC, cron, cluster
│   ├── domain/                 # Custom domains, Traefik ingress
│   ├── certificate/            # TLS/ACME certificate management
│   ├── secret/                 # AES-256-GCM envelope encryption
│   ├── logging/                # Centralized log ingestion
│   ├── metrics/                # Prometheus exposition & alerts
│   ├── storage/                # MinIO S3 object storage
│   ├── database/               # Managed PostgreSQL schemas
│   ├── ai/                     # OpenAI diagnosis & heuristics
│   ├── billing/                # Usage metering & plans
│   ├── addon/                  # 241+ marketplace engines
│   └── event/                  # Redis Streams event bus
│
├── packages/                   # Shared libraries
│   ├── go-common/              # HTTP, JWT, middleware, Postgres, Redis, OTel
│   ├── events/                 # Event envelope & Streams helpers
│   ├── openapi/                # OpenAPI 3.0 specification
│   ├── jp-schema/              # jp.yaml JSON Schema
│   └── templates/              # Project scaffolding templates
│
├── infra/
│   ├── compose/                # Docker Compose stack
│   │   ├── docker-compose.yml  # Main compose (1,100+ lines, 6 profiles)
│   │   └── sizing/             # Resource overlays (lite→xlarge)
│   ├── docker/                 # Dockerfiles (multi-stage Alpine)
│   ├── monitoring/             # Prometheus, Grafana, Loki, Tempo configs
│   └── traefik/                # Edge proxy & dynamic routing configs
│
├── scripts/                    # Automation scripts
│   ├── size-host.*             # Host capacity detection
│   ├── jp-up.*                 # Smart platform launcher
│   ├── go-real.*               # Switch to real mode
│   ├── test-all.*              # Run all tests
│   └── setup-server.sh         # VPS setup automation
│
├── docs/                       # 18 documentation files
│   ├── ARCHITECTURE.md         # System architecture
│   ├── FEATURE_STATUS.md       # Feature matrix (verified/partial/missing)
│   ├── INSTALLATION.md         # Production installation guide
│   ├── SECURITY.md             # Security architecture
│   ├── EXTENSIBILITY.md        # Plugin & extension cookbook
│   ├── DEVELOPMENT.md          # Developer guide
│   ├── TESTING.md              # Test strategy
│   └── ...                     # Operations, backup, scaling, etc.
│
├── .github/workflows/          # CI/CD pipelines
│   ├── ci.yml                  # Test + build + validate
│   └── release.yml             # Multi-arch release + SBOM
│
├── install.sh / install.ps1    # One-command installers
├── uninstall.sh / uninstall.ps1 # Clean uninstallers
├── go.work                     # Go workspace (23 modules)
└── .env.example                # Environment template (155 variables)
```

---

## 🔄 Real vs Simulated Modes

Defaults are **real**. You can simulate individual subsystems for development without Docker:

| Subsystem | Real Mode (Default) | Simulated Mode |
|---|---|---|
| **Builds** | Docker BuildKit via worker | Synthetic log stream (`BUILD_MODE=simulate`) |
| **Runtime** | Docker Engine via socket | Mock containers (`RUNTIME_MODE=simulate`) |
| **Storage** | MinIO S3 buckets | In-memory mock (`STORAGE_MODE=simulate`) |
| **Databases** | PostgreSQL schema provisioning | Stub (`DB_MODE=simulate`) |
| **Domains** | Live DNS verification | Stub (`DOMAIN_DNS_STUB=true`) |
| **Certificates** | ACME Let's Encrypt | Mock (`CERT_SIMULATE=true`) |
| **GitHub** | Real GitHub App + webhooks | Stub when `GITHUB_APP_ID` not set |
| **AI** | OpenAI API (`gpt-4o-mini`) | Heuristic fallback when no API key |
| **Metrics** | Prometheus + live scraping | — (`METRICS_MODE=live` is default) |

```bash
# Switch everything to real mode
./scripts/go-real.sh   # or .\scripts\go-real.ps1
```

---

## 🔒 Security

- **AES-256-GCM** envelope encryption for all secrets at rest
- **bcrypt** (cost=10) password hashing
- **Dual JWT lifecycle**: 15-minute access tokens + 7-day refresh tokens
- **SHA-256 hashed** Personal Access Tokens — displayed once, stored as hashes
- **TOTP multi-factor authentication** (RFC 6238) with backup codes
- **Redis-based rate limiting**: 5 login attempts per 15 minutes, HTTP 429 lockout
- **HMAC-SHA256** GitHub webhook signature verification
- **Non-root containers**: Services run as unprivileged user (UID 10001)
- **Docker socket isolation**: Accessible only to internal `worker` and `runtime` services
- **Private-by-default networking**: All services communicate on internal Docker bridge network
- **CORS allowlisting**: Strict origin validation
- **Image vulnerability scanning**: Trivy-based container security scanning
- **Security posture dashboard**: Hardening checklist, active findings, remediation

See the full [Security Architecture](docs/SECURITY.md) document.

---

## 🛠️ Development

### Running Services Locally

```bash
# Start infrastructure dependencies
docker compose -f infra/compose/docker-compose.yml --env-file .env up -d postgres redis

# Run a service directly
cd services/gateway && go run ./cmd

# Run CLI
cd apps/cli && go run . login

# Dashboard dev server
cd apps/dashboard && npm install && npm run dev
```

### Running Tests

```bash
# All Go tests
./scripts/test-all.sh        # or .\scripts\test-all.ps1

# Individual service
cd services/addon && go test ./...

# Dashboard
cd apps/dashboard && npm run lint
```

### Adding a New Microservice

1. Create `services/<name>/` with `cmd/`, `internal/`, `migrations/` directories
2. Initialize `go.mod` and add to `go.work`
3. Register in `infra/compose/docker-compose.yml`
4. Add gateway route in `services/gateway/internal/proxy/`

See the full [Extensibility Guide](docs/EXTENSIBILITY.md).

---

## 📚 Documentation

| Document | Description |
|---|---|
| [Architecture](docs/ARCHITECTURE.md) | System design, subsystem boundaries, request flow |
| [Feature Status](docs/FEATURE_STATUS.md) | Complete feature matrix with verification status |
| [Installation](docs/INSTALLATION.md) | Production server setup runbook |
| [Security](docs/SECURITY.md) | Cryptography, identity, container hardening |
| [Extensibility](docs/EXTENSIBILITY.md) | Plugin architecture, add-on development |
| [Development](docs/DEVELOPMENT.md) | Developer setup & contribution guide |
| [Testing](docs/TESTING.md) | Test strategy & running tests |
| [Operations](docs/OPERATIONS.md) | Day-2 operations & maintenance |
| [Backup & Restore](docs/BACKUP_RESTORE.md) | Data backup procedures |
| [App Catalog](docs/APP_CATALOG.md) | Marketplace engine catalog |
| [Scale-Out](docs/scale-out.md) | Multi-node scaling architecture |
| [VPS Runbook](docs/vps-runbook.md) | Quick VPS deployment guide |
| [Full Reference](docs/full-platform-reference.md) | Complete platform specification |

---

## 🗺️ Roadmap

| Phase | Scope | Status |
|---|---|---|
| **Phase 0** | Monorepo, shared packages, Compose, Traefik | ✅ Complete |
| **Phase 1** | Auth, orgs, projects, PATs, gateway, notifications | ✅ Complete |
| **Phase 2** | Repos, webhooks, deployments, rollback, dashboard/CLI | ✅ Complete |
| **Phase 3** | Build farm, worker, registry:2, image metadata | ✅ Complete |
| **Phase 4** | Runtime, scheduler, domains, certificates | ✅ Complete |
| **Phase 5** | Secrets, environments, logs, metrics, monitoring stack | ✅ Complete |
| **Phase 6** | Storage (MinIO), databases, cleanup queues, cron jobs | ✅ Complete |
| **Phase 7** | jp.yaml IaC, deploy strategies, AI ops, billing | ✅ Complete |
| **Phase 8** | Scale-out, multi-node clustering | ✅ Complete |

---

## 🤝 Contributing

Contributions are welcome! Here's how to get started:

1. **Fork** the repository
2. **Clone** your fork locally
3. **Create** a feature branch: `git checkout -b feature/your-feature`
4. **Make** your changes following the existing code style
5. **Test** your changes: `./scripts/test-all.sh`
6. **Commit** with a descriptive message
7. **Push** and open a Pull Request

Please read the [Development Guide](docs/DEVELOPMENT.md) and [Extensibility Guide](docs/EXTENSIBILITY.md) before contributing.

---

## 📄 License

This project is open source. See the repository for license details.

---

<p align="center">
  <strong>Built with ❤️ by <a href="https://github.com/zeeshan-890">Zeeshan Abbas Khan</a></strong>
</p>
