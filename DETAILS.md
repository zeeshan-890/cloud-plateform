# jp Cloud Platform — Complete Technical Architecture, Internals & Developer Manual

> **Ultra-Detailed System Reference, Codebase Specification & Developer Extension Guide.**  
> This document contains every single piece of information required to deeply understand, operate, debug, modify, and extend the `jp` Cloud Platform monorepo.

---

## Table of Contents

1. [Architectural Philosophy & System Topography](#1-architectural-philosophy--system-topography)
   - 1.1 [Platform Topology & High-Level Architecture](#11-platform-topology--high-level-architecture)
   - 1.2 [Request Flow & Lifecycle (End-to-End)](#12-request-flow--lifecycle-end-to-end)
   - 1.3 [Control Plane vs. Data Plane Separation](#13-control-plane-vs-data-plane-separation)
2. [Shared Foundations (`packages/go-common`)](#2-shared-foundations-packagesgo-common)
   - 2.1 [Configuration Loader (`config`)](#21-configuration-loader-config)
   - 2.2 [HTTP Utilities & Standard Error Contract (`httpx`)](#22-http-utilities--standard-error-contract-httpx)
   - 2.3 [PostgreSQL Pool & Migration Engine (`postgres`)](#23-postgresql-pool--migration-engine-postgres)
   - 2.4 [JWT Manager & Session Claims (`jwt`)](#24-jwt-manager--session-claims-jwt)
   - 2.5 [Middleware Stack (`middleware`)](#25-middleware-stack-middleware)
   - 2.6 [Distributed Tracing & OpenTelemetry (`otelx`)](#26-distributed-tracing--opentelemetry-otelx)
   - 2.7 [Structured Logging (`logging`)](#27-structured-logging-logging)
   - 2.8 [Security Audit Emitter (`audit`)](#28-security-audit-emitter-audit)
   - 2.9 [IaC Manifest Engine (`jpconfig`)](#29-iac-manifest-engine-jpconfig)
3. [Event Bus & Streaming Internals (`packages/events`)](#3-event-bus--streaming-internals-packagesevents)
   - 3.1 [Redis Streams Architecture & Topics](#31-redis-streams-architecture--topics)
   - 3.2 [Event Envelope Schema & Type Constants](#32-event-envelope-schema--type-constants)
   - 3.3 [Consumer Group Mechanics & Delivery Semantics](#33-consumer-group-mechanics--delivery-semantics)
   - 3.4 [Publishing & Consumption Code Patterns](#34-publishing--consumption-code-patterns)
4. [Ingress & Edge Routing (Traefik v3.2)](#4-ingress--edge-routing-traefik-v32)
   - 4.1 [Static Configuration & Entrypoints](#41-static-configuration--entrypoints)
   - 4.2 [Dynamic File Provider Engine](#42-dynamic-file-provider-engine)
   - 4.3 [Dynamic Host Ingress & Router Synthesis](#43-dynamic-host-ingress--router-synthesis)
   - 4.4 [ACME TLS Certificate Resolver](#44-acme-tls-certificate-resolver)
5. [Façade Gateway Architecture (`services/gateway`)](#5-façade-gateway-architecture-servicesgateway)
   - 5.1 [Reverse Proxy & URL Rewriting](#51-reverse-proxy--url-rewriting)
   - 5.2 [Authentication & PAT Exchanger Pipeline](#52-authentication--pat-exchanger-pipeline)
   - 5.3 [Multi-Tenant RBAC Context Resolution](#53-multi-tenant-rbac-context-resolution)
   - 5.4 [Downstream Header Injections](#54-downstream-header-injections)
6. [Complete Services Catalog & Internal Code Architectures (23 Microservices)](#6-complete-services-catalog--internal-code-architectures-23-microservices)
   - 6.1 [identity (:8001)](#61-identity-8001)
   - 6.2 [organization (:8002)](#62-organization-8002)
   - 6.3 [project (:8003)](#63-project-8003)
   - 6.4 [notification (:8004)](#64-notification-8004)
   - 6.5 [repository (:8005)](#65-repository-8005)
   - 6.6 [deployment (:8006)](#66-deployment-8006)
   - 6.7 [build (:8007)](#67-build-8007)
   - 6.8 [worker (:8008)](#68-worker-8008)
   - 6.9 [registry-api (:8009) & registry:2 (:5000)](#69-registry-api-8009--registry2-5000)
   - 6.10 [runtime (:8010)](#610-runtime-8010)
   - 6.11 [scheduler (:8011)](#611-scheduler-8011)
   - 6.12 [domain (:8012)](#612-domain-8012)
   - 6.13 [certificate (:8013)](#613-certificate-8013)
   - 6.14 [secret (:8014)](#614-secret-8014)
   - 6.15 [logging (:8015)](#615-logging-8015)
   - 6.16 [metrics (:8016)](#616-metrics-8016)
   - 6.17 [storage (:8017)](#617-storage-8017)
   - 6.18 [database (:8018)](#618-database-8018)
   - 6.19 [addon (:8021)](#619-addon-8021)
   - 6.20 [ai (:8019)](#620-ai-8019)
   - 6.21 [billing (:8020)](#621-billing-8020)
   - 6.22 [event (packages/events)](#622-event-packagesevents)
7. [Database Schemas, DDL & Migration Engine](#7-database-schemas-ddl--migration-engine)
   - 7.1 [Entity Relationship Diagram (ERD)](#71-entity-relationship-diagram-erd)
   - 7.2 [Migration File Ordering & Execution Engine](#72-migration-file-ordering--execution-engine)
   - 7.3 [Complete DDL for Every Service Table](#73-complete-ddl-for-every-service-table)
8. [Exhaustive API Route & Request/Response Specification](#8-exhaustive-api-route--requestresponse-specification)
   - 8.1 [Auth & PAT API](#81-auth--pat-api)
   - 8.2 [Organization & Team RBAC API](#82-organization--team-rbac-api)
   - 8.3 [Project & IaC API](#83-project--iac-api)
   - 8.4 [Git & Webhook Receiver API](#84-git--webhook-receiver-api)
   - 8.5 [Deployment & Rollback API](#85-deployment--rollback-api)
   - 8.6 [Build & Registry API](#86-build--registry-api)
   - 8.7 [Runtime & Container API](#87-runtime--container-api)
   - 8.8 [Domain & Ingress Routing API](#88-domain--ingress-routing-api)
   - 8.9 [TLS Certificate API](#89-tls-certificate-api)
   - 8.10 [Environments & Encrypted Secrets API](#810-environments--encrypted-secrets-api)
   - 8.11 [Telemetry Logging API](#811-telemetry-logging-api)
   - 8.12 [Metrics & Prometheus API](#812-metrics--prometheus-api)
   - 8.13 [MinIO S3 Storage API](#813-minio-s3-storage-api)
   - 8.14 [Managed Relational Database API](#814-managed-relational-database-api)
   - 8.15 [Backing Add-ons Marketplace API](#815-backing-add-ons-marketplace-api)
   - 8.16 [Cron Schedules & Platform Queue API](#816-cron-schedules--platform-queue-api)
   - 8.17 [AI Ops Diagnostic API](#817-ai-ops-diagnostic-api)
   - 8.18 [Billing & Metering API](#818-billing--metering-api)
9. [CLI Architecture & Command Implementation (`apps/cli`)](#9-cli-architecture--command-implementation-appscli)
   - 9.1 [CLI Anatomy & Configuration Storage](#91-cli-anatomy--configuration-storage)
   - 9.2 [Command Tree, Flag Bindings & Pre-Run Validations](#92-command-tree-flag-bindings--pre-run-validations)
   - 9.3 [HTTP API Client Internals (`internal/client`)](#93-http-api-client-internals-internalclient)
   - 9.4 [Local Template Generators & YAML Scaffolding](#94-local-template-generators--yaml-scaffolding)
10. [Dashboard Web Console Architecture (`apps/dashboard`)](#10-dashboard-web-console-architecture-appsdashboard)
    - 10.1 [Next.js 16 App Router Structure & Layouts](#101-nextjs-16-app-router-structure--layouts)
    - 10.2 [Context Providers & Authentication Life Cycle](#102-context-providers--authentication-life-cycle)
    - 10.3 [API Client with Deadlock-Free Token Refresh](#103-api-client-with-deadlock-free-token-refresh)
    - 10.4 [UI Component Tree & Page Specifications](#104-ui-component-tree--page-specifications)
11. [Data Plane Execution & Build Farm Pipeline](#11-data-plane-execution--build-farm-pipeline)
    - 11.1 [Worker Engine & BuildKit Pipeline](#111-worker-engine--buildkit-pipeline)
    - 11.2 [OCI Image Registry Flow (`registry:2` & `registry-api`)](#112-oci-image-registry-flow-registry2--registry-api)
    - 11.3 [Runtime Engine & Docker Daemon Orchestration](#113-runtime-engine--docker-daemon-orchestration)
    - 11.4 [Scheduler Loop, Rolling Replaces & Blue/Green Deployments](#114-scheduler-loop-rolling-replaces--bluegreen-deployments)
12. [Envelope Encryption & Security Architecture](#12-envelope-encryption--security-architecture)
    - 12.1 [AES-256-GCM Envelope Encryption Deep Dive](#121-aes-256-gcm-envelope-encryption-deep-dive)
    - 12.2 [Nonce Generation & Key Versioning](#122-nonce-generation--key-versioning)
    - 12.3 [Zero-Plaintext Leak Guarantees](#123-zero-plaintext-leak-guarantees)
13. [Hardware Sizing & Resource Optimization Engine](#13-hardware-sizing--resource-optimization-engine)
    - 13.1 [Host Memory/CPU Detection Engine](#131-host-memorycpu-detection-engine)
    - 13.2 [Compose Overlay Architecture](#132-compose-overlay-architecture)
    - 13.3 [Worker Concurrency & Worker Scaling](#133-worker-concurrency--worker-scaling)
14. [Real vs. Simulate Execution Matrix](#14-real-vs-simulate-execution-matrix)
15. [Master Environment Variable & Configuration Directory](#15-master-environment-variable--configuration-directory)
16. [Developer Extension Cookbook (Step-by-Step Guides)](#16-developer-extension-cookbook-step-by-step-guides)
    - 16.1 [How to Add a New Microservice](#161-how-to-add-a-new-microservice)
    - 16.2 [How to Add a New API Route](#162-how-to-add-a-new-api-route)
    - 16.3 [How to Add a New Database Table & SQL Migration](#163-how-to-add-a-new-database-table--sql-migration)
    - 16.4 [How to Add a New Event Topic & Consumer Group](#164-how-to-add-a-new-event-topic--consumer-group)
    - 16.5 [How to Add a New Add-on Engine to Marketplace](#165-how-to-add-a-new-add-on-engine-to-marketplace)
    - 16.6 [How to Add a New CLI Command](#166-how-to-add-a-new-cli-command)
    - 16.7 [How to Add a New Dashboard Page & Component](#167-how-to-add-a-new-dashboard-page--component)
    - 16.8 [How to Connect Real Stripe Billing](#168-how-to-connect-real-stripe-billing)
17. [Operational Runbook, Troubleshooting & Diagnostics](#17-operational-runbook-troubleshooting--diagnostics)
    - 17.1 [Local Quickstart (Windows & Linux/macOS)](#171-local-quickstart-windows--linuxmacos)
    - 17.2 [Diagnostic Commands & Health Checks](#172-diagnostic-commands--health-checks)
    - 17.3 [Common Failure Modes & Recovery Procedures](#173-common-failure-modes--recovery-procedures)

---

## 1. Architectural Philosophy & System Topography

### 1.1 Platform Topology & High-Level Architecture

`jp` is an autonomous, self-hosted Platform-as-a-Service (PaaS) monorepo designed with a clean separation between **Façade**, **Control Plane**, **Data Plane**, and **Persistence**.

```mermaid
flowchart TB
  subgraph EdgeTier["Edge Ingress Tier"]
    Traefik["Traefik v3.2 Edge Proxy (:80, :443, :8080)"]
  end

  subgraph FacadeTier["Façade Tier"]
    GW["gateway (:8000)"]
  end

  subgraph ControlPlane["Control Plane Services"]
    ID["identity (:8001)"]
    ORG["organization (:8002)"]
    PRJ["project (:8003)"]
    NOTIF["notification (:8004)"]
    REPO["repository (:8005)"]
    DEP["deployment (:8006)"]
    BLD["build (:8007)"]
    REGAPI["registry-api (:8009)"]
    DOM["domain (:8012)"]
    CERT["certificate (:8013)"]
    SEC["secret (:8014)"]
    LOG["logging (:8015)"]
    MET["metrics (:8016)"]
    STOR["storage (:8017)"]
    DB["database (:8018)"]
    ADDON["addon (:8021)"]
    AI["ai (:8019)"]
    BILL["billing (:8020)"]
  end

  subgraph DataPlane["Data Plane Tier"]
    WRK["worker × N (:8008)"]
    OCI["registry:2 (:5000)"]
    SCH["scheduler (:8011)"]
    RT["runtime (:8010)"]
    DockerDaemon[("/var/run/docker.sock")]
  end

  subgraph PersistenceTier["Persistence & Event Bus Tier"]
    PG[("PostgreSQL 16")]
    Redis[("Redis 7 (Streams + Cache)")]
    MinIO[("MinIO (:9000/:9001)")]
  end

  %% Client Routing
  Traefik -->|PathPrefix `/api`| GW
  Traefik -->|Dynamic Host Routers| RT

  %% Gateway Proxying
  GW --> ID & ORG & PRJ & NOTIF & REPO & DEP & BLD & REGAPI & DOM & CERT & SEC & LOG & MET & STOR & DB & ADDON & SCH & AI & BILL

  %% Asynchronous Event Streams
  DEP -->|XADD jp.deploy| Redis
  BLD -->|XADD jp.build| Redis
  Redis -->|XREADGROUP jp-workers| WRK
  Redis -->|XREADGROUP jp-scheduler| SCH
  Redis -->|XREADGROUP jp-billing| BILL

  %% Data Plane Actions
  WRK -->|Docker BuildKit| DockerDaemon
  WRK -->|Push Image| OCI
  SCH -->|POST /internal/runtime/start| RT
  RT -->|Docker Run/Stop| DockerDaemon
  STOR --> MinIO
```

### 1.2 Request Flow & Lifecycle (End-to-End)

1. **Client Ingress:** A client (Dashboard browser, `jp` CLI, or GitHub webhook) issues an HTTP request to `http://localhost:8000/api/v1/...` (or port `80` through Traefik).
2. **Edge Processing:** Traefik matches the `PathPrefix('/api')` rule and forwards the connection to `gateway:8000`.
3. **Trace Context Propagation:** `gateway` reads or mints W3C trace headers (`traceparent`, `X-Trace-ID`, `X-Request-ID`).
4. **Authentication Check:**
   - If public path, request proceeds immediately.
   - If `Authorization: Bearer jp_pat_*`, gateway performs internal RPC to `identity:8001/internal/pats/verify`. Once confirmed, gateway mints a short-lived access JWT and attaches claims to request context.
   - If Bearer JWT, claims are verified against `JWT_SECRET`.
5. **Organization Membership Lookup:**
   - If the route pattern contains `{orgId}`, the gateway queries `organization:8002/internal/orgs/{orgId}/members/{userId}`.
   - The user's role (`owner`, `admin`, `member`, `viewer`) is retrieved and injected as `X-Org-Role` and `X-Org-ID`.
6. **Reverse Proxy Dispatch:** `gateway` strips the `/api/v1` prefix and proxies the sanitized request to the target microservice.
7. **Downstream Execution:** The microservice executes business logic, reads/writes PostgreSQL via `pgxpool`, publishes domain events to Redis Streams, and returns a standard JSON payload.

### 1.3 Control Plane vs. Data Plane Separation

- **Control Plane (`identity`, `organization`, `project`, `secret`, `database`, `billing`, etc.):** Pure business logic, metadata cataloging, authentication, and state management. Control plane services never execute untrusted user code or interface directly with host operating system primitives.
- **Data Plane (`worker`, `runtime`, `scheduler`, `registry:2`):** Directly manages the lifecycle of containerized workloads. Interfaces directly with Docker BuildKit, host Docker sockets, and local OCI registries. Operates asynchronously via Redis consumer groups.

---

## 2. Shared Foundations (`packages/go-common`)

All Go microservices and the CLI share a unified, battle-tested standard library located at `packages/go-common`.

### 2.1 Configuration Loader (`config`)
Located at `packages/go-common/config/config.go`. Loads environment variables with fallback defaults:
```go
type Config struct {
    HTTPPort         string
    LogLevel         string
    DatabaseURL      string
    RedisURL         string
    JWTSecret        string
    JWTAccessTTL     time.Duration
    JWTRefreshTTL    time.Duration
    CORSOrigins      []string
    IdentityURL      string
    OrganizationURL  string
    ProjectURL       string
    // ... all other service URLs
}

func Load(defaultPort string) (*Config, error)
```
- Reads `PORT` environment variable, falling back to `defaultPort`.
- Parses durations for JWT TTLs (`15m`, `168h`).
- Splits `CORS_ORIGINS` by commas into clean string slices.

### 2.2 HTTP Utilities & Standard Error Contract (`httpx`)
Located at `packages/go-common/httpx/httpx.go`. Enforces uniform JSON response schemas across all endpoints:

```go
type ErrorBody struct {
    Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
    Code    string `json:"code"`
    Message string `json:"message"`
}

// JSON writes any value as application/json with the given status code.
func JSON(w http.ResponseWriter, status int, v any)

// Error writes a standardized ErrorBody with code and message.
func Error(w http.ResponseWriter, status int, code, message string)

// Decode parses the request body and enforces DisallowUnknownFields().
func Decode(r *http.Request, dst any) error

// Healthz is a standardized liveness handler returning {"status":"ok"}.
func Healthz(w http.ResponseWriter, _ *http.Request)
```

### 2.3 PostgreSQL Pool & Migration Engine (`postgres`)
Located at `packages/go-common/postgres/postgres.go`.
- **Pool Management:** Configures `pgxpool.Pool` with `MaxConns = 10`, `MinConns = 1`, and `MaxConnLifetime = 1h`.
- **Automated Migration Runner:**
  1. Creates table `schema_migrations (filename TEXT PRIMARY KEY, applied_at TIMESTAMPTZ)`.
  2. Reads all `.sql` files in the specified directory.
  3. Sorts file names lexicographically (e.g. `project_001_init.sql`, `project_002_jp_config.sql`).
  4. Wraps each unapplied migration in an explicit SQL transaction (`tx.Begin()`, executes DDL, records in `schema_migrations`, `tx.Commit()`).

### 2.4 JWT Manager & Session Claims (`jwt`)
Located at `packages/go-common/jwt/jwt.go`.
- Uses `golang-jwt/jwt/v5` with HMAC-SHA256 (`HS256`).
- Distinguishes between token types via the `type` claim:
  - `access`: Short-lived (`15m`). Contains `user_id`, `email`, `name`.
  - `refresh`: Long-lived (`7d`). Contains `user_id` and unique `jti` (JWT ID).
- Methods:
  - `IssueAccess(userID, email, name string) (token string, exp time.Time, err error)`
  - `IssueRefresh(userID string) (token string, jti string, exp time.Time, err error)`
  - `Parse(tokenStr string) (*Claims, error)`

### 2.5 Middleware Stack (`middleware`)
Located at `packages/go-common/middleware/middleware.go`. Standard functional chaining:
- `Chain(handler, ...middleware)`: Composes middlewares in left-to-right execution order.
- `RequestID`: Checks incoming `X-Request-ID` or generates a fresh UUID. Attaches ID to context and response headers.
- `Logging(logger)`: Records HTTP method, path, remote IP, status code, and latency in milliseconds.
- `CORS(origins)`: Handles preflight `OPTIONS` requests, allows methods `GET, POST, PUT, PATCH, DELETE, OPTIONS`, allows headers `Authorization, Content-Type, X-Request-ID, X-Trace-ID`.
- `BearerAuth(jm)`: Validates JWT token from `Authorization: Bearer <token>`, extracts claims, and injects `ClaimsKey` and `UserIDKey` into context.

### 2.6 Distributed Tracing & OpenTelemetry (`otelx`)
Located at `packages/go-common/otelx/otelx.go`.
- Ingests W3C TraceContext headers (`traceparent`).
- Extracts or generates 16-byte hex Trace ID and 8-byte hex Span ID.
- Propagates context downstream across internal HTTP calls.

### 2.7 Structured Logging (`logging`)
Located at `packages/go-common/logging/logging.go`.
- Wraps Go standard library `log/slog`.
- Outputs structured JSON format to `os.Stdout`.
- Supports log levels: `DEBUG`, `INFO`, `WARN`, `ERROR`.

### 2.8 Security Audit Emitter (`audit`)
Located at `packages/go-common/audit/audit.go`.
- Dispatches security audit entries (`actor_id`, `action`, `resource`, `resource_id`, `metadata`) to PostgreSQL `audit_logs` or Redis Streams topic `jp.audit`.

### 2.9 IaC Manifest Engine (`jpconfig`)
Located at `packages/go-common/jpconfig/jpconfig.go`.
- Parses, unmarshals, and validates `jp.yaml` files.
- Computes canonical SHA-256 hashes of manifest configurations for drift detection.

---

## 3. Event Bus & Streaming Internals (`packages/events`)

The platform's asynchronous messaging backbone is implemented on **Redis Streams** (`packages/events`).

### 3.1 Redis Streams Architecture & Topics

| Topic Constant | Stream Name | Emitting Services | Consuming Services |
|---|---|---|---|
| `TopicAuth` | `jp.auth` | `identity` | `notification`, `audit` |
| `TopicOrg` | `jp.org` | `organization` | `notification`, `billing` |
| `TopicProject` | `jp.project` | `project` | `storage`, `scheduler` |
| `TopicNotification` | `jp.notification` | All services | `notification` |
| `TopicAudit` | `jp.audit` | All services | `identity` (Audit Log Store) |
| `TopicDeploy` | `jp.deploy` | `deployment` | `scheduler`, `billing` |
| `TopicBuild` | `jp.build` | `build` | `worker`, `billing` |
| `TopicRuntime` | `jp.runtime` | `runtime`, `scheduler` | `logging`, `billing` |
| `TopicDomain` | `jp.domain` | `domain` | `certificate`, Traefik reloader |
| `TopicSecret` | `jp.secret` | `secret` | `audit`, `runtime` |
| `TopicLogging` | `jp.logging` | `logging` | `ai`, Loki shipper |
| `TopicMetrics` | `jp.metrics` | `metrics` | Prometheus exporter |
| `TopicStorage` | `jp.storage` | `storage` | `cleanup` |
| `TopicDatabase` | `jp.database` | `database` | `secret`, `billing` |
| `TopicAddon` | `jp.addon` | `addon` | `secret`, `billing` |
| `TopicCleanup` | `jp.cleanup` | `scheduler` | `runtime`, `registry` |
| `TopicJobs` | `jp.jobs` | `scheduler` (Cron) | Background task workers |

### 3.2 Event Envelope Schema & Type Constants

Every stream message contains a single key `payload` whose value is a serialized JSON `Envelope`:

```go
type Envelope struct {
    ID        string         `json:"id"`
    Type      string         `json:"type"`
    Topic     string         `json:"topic"`
    Timestamp time.Time      `json:"timestamp"`
    ActorID   string         `json:"actor_id,omitempty"`
    OrgID     string         `json:"org_id,omitempty"`
    Payload   map[string]any `json:"payload"`
}
```

#### Event Type Constants (`packages/events/events.go`):
- `user.registered`, `user.logged_in`, `user.logged_out`
- `org.invite_created`, `org.invite_accepted`
- `project.created`, `project.updated`, `project.deleted`
- `deploy.created`, `deploy.updated`
- `build.queued`, `build.started`, `build.succeeded`, `build.failed`
- `runtime.started`, `runtime.stopped`, `runtime.failed`
- `domain.added`, `domain.verified`
- `cert.issued`, `cert.renewed`
- `secret.created`, `secret.rotated`, `secret.deleted`
- `storage.uploaded`, `storage.deleted`
- `database.created`, `database.deleted`
- `addon.created`, `addon.deleted`
- `cleanup.orphaned_images`, `cleanup.preview_deploys`
- `cron.triggered`, `job.queued`

### 3.3 Consumer Group Mechanics & Delivery Semantics

- **Consumer Groups:**
  - `jp-workers`: Assigned to `jp.build`. Scaled across $N$ worker replicas.
  - `jp-scheduler`: Assigned to `jp.deploy`. Evaluates ready deployments.
  - `jp-cleanup`: Assigned to `jp.cleanup`. Executes garbage collection.
  - `jp-jobs`: Assigned to `jp.jobs`. Executes scheduled cron tasks.
  - `jp-billing`: Assigned to `jp.build` and `jp.deploy`. Meters consumption.
- **Delivery Guarantee:** At-least-once delivery.
- **Message Acknowledgment:** Workers read messages via `XREADGROUP ... STREAMS topic >`. Upon successful processing, workers issue `XACK topic group message_id`. If a worker crashes before acknowledging, the message remains in the Pending Entries List (PEL) for redelivery.

### 3.4 Publishing & Consumption Code Patterns

#### Publishing an Event:
```go
env := events.New(events.TopicBuild, events.TypeBuildQueued, actorID, orgID, map[string]any{
    "build_id": buildID,
    "project_id": projectID,
    "git_sha": gitSHA,
})
id, err := events.PublishJSON(ctx, rdb, events.TopicBuild, env)
```

#### Consuming from a Consumer Group:
```go
// 1. Ensure consumer group exists
err := events.EnsureGroup(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup)

// 2. Poll loop
for {
    msgs, err := events.ReadGroup(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup, workerName, 1, 2*time.Second)
    if err != nil || len(msgs) == 0 {
        continue
    }
    for _, msg := range msgs {
        env, err := events.ParseEnvelope(msg)
        if err != nil {
            continue
        }
        // Execute work...
        _ = events.Ack(ctx, rdb, events.TopicBuild, events.BuildConsumerGroup, msg.ID)
    }
}
```

---

## 4. Ingress & Edge Routing (Traefik v3.2)

### 4.1 Static Configuration & Entrypoints

Configured at `infra/traefik/traefik.yml`:
- Entrypoint `web`: Listens on port `80`.
- Entrypoint `traefik`: Internal management dashboard listening on port `8080`.
- Providers:
  - `docker`: Reads labels from running containers (`exposedByDefault: false`).
  - `file`: Directory watch on `/etc/traefik/dynamic` with `watch: true`.

### 4.2 Dynamic File Provider Engine

Traefik dynamically reads routing files without requiring proxy reboots:
- `infra/traefik/dynamic/gateway.yml`:
  ```yaml
  http:
    routers:
      gateway-router:
        rule: "PathPrefix(`/api`)"
        entryPoints: ["web"]
        service: "gateway-service"
    services:
      gateway-service:
        loadBalancer:
          servers:
            - url: "http://gateway:8000"
  ```

### 4.3 Dynamic Host Ingress & Router Synthesis

When a user binds a custom domain (e.g. `api.customer.com`) and verifies DNS, the `domain` service writes a dynamic configuration file directly into `/etc/traefik/dynamic/domain-{domain_id}.yml`:

```yaml
http:
  routers:
    router-api-customer-com:
      rule: "Host(`api.customer.com`)"
      entryPoints: ["web"]
      service: "service-api-customer-com"
  services:
    service-api-customer-com:
      loadBalancer:
        servers:
          - url: "http://runtime:8010"
```
Traefik's inotify watcher immediately ingests the file and routes traffic for `api.customer.com` directly to the container runtime.

### 4.4 ACME TLS Certificate Resolver

When `TRAEFIK_CERT_RESOLVER=letsencrypt` is configured, Traefik automatically requests certificates from Let's Encrypt using HTTP-01 or TLS-ALPN-01 challenges, persisting certificates in an encrypted volume.

---

## 5. Façade Gateway Architecture (`services/gateway`)

### 5.1 Reverse Proxy & URL Rewriting

The `gateway` service operates as a transparent reverse proxy implemented using `net/http/httputil.ReverseProxy`:
```go
func stripAndProxy(upstream http.Handler, prefix string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        r2 := r.Clone(r.Context())
        r2.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
        if r2.URL.Path == "" {
            r2.URL.Path = "/"
        }
        upstream.ServeHTTP(w, r2)
    }
}
```

### 5.2 Authentication & PAT Exchanger Pipeline

```mermaid
sequenceDiagram
  autonumber
  actor Client as CLI / Dashboard
  participant GW as gateway (:8000)
  participant ID as identity (:8001)
  participant Sub as Downstream Service

  Client->>GW: Request with Authorization: Bearer <token>
  alt Token starts with jp_pat_*
    GW->>ID: POST /internal/pats/verify {token: "jp_pat_..."}
    ID->>ID: SHA-256 hash lookup in personal_access_tokens
    ID-->>GW: 200 OK { user: { id, email, name } }
    GW->>GW: Mint short-lived access JWT with user claims
    GW->>GW: Replace Authorization header with Bearer <jwt>
  else Token is standard JWT
    GW->>GW: jwtutil.Manager.Parse(token)
    GW->>GW: Verify type == "access" & signature
  end
  GW->>GW: Context = context.WithValue(Context, UserID, claims.UserID)
  GW->>Sub: Strip /api/v1 + Inject X-User-ID, X-User-Email
  Sub-->>GW: Response JSON
  GW-->>Client: Response JSON
```

### 5.3 Multi-Tenant RBAC Context Resolution

For every organization-scoped request (`/orgs/{orgId}/...`), the gateway checks authorization before proxying:
1. Extracts `orgId` path variable from the URL.
2. Calls internal endpoint: `GET http://organization:8002/internal/orgs/{orgId}/members/{userId}`.
3. If response is `404 Not Found`, the user is not in the organization; gateway immediately aborts with `403 Forbidden`.
4. If response is `200 OK`, extracts `role` (`owner`, `admin`, `member`, `viewer`).
5. Sets HTTP request headers:
   - `X-Org-ID: {orgId}`
   - `X-Org-Role: {role}`

### 5.4 Downstream Header Injections

Downstream microservices never need to parse raw tokens or query organization membership. They rely entirely on headers injected by the gateway:
- `X-User-ID`: Validated user UUID.
- `X-User-Email`: User email address.
- `X-Org-ID`: Active organization UUID.
- `X-Org-Role`: Active role within the organization.
- `X-Request-ID`: Correlation identifier.
- `traceparent`: OpenTelemetry distributed trace context.

---

## 6. Complete Services Catalog & Internal Code Architectures (23 Microservices)

Every service follows an identical, clean internal package structure:
```
services/<service-name>/
├── cmd/main.go               # Entrypoint: config, pool, migrate, router, server
├── internal/
│   ├── handlers/handlers.go # HTTP route registrations and handler logic
│   └── store/store.go       # SQL database queries and data mapping
├── migrations/              # Lexicographically sorted SQL migration scripts
├── README.md
├── go.mod
└── go.sum
```

### 6.1 identity (:8001)
- **Port:** `8001`
- **Tables Owned:** `users`, `sessions`, `personal_access_tokens`, `audit_logs`
- **Key Files:** `internal/handlers/auth.go`, `internal/handlers/pats.go`, `internal/store/users.go`
- **Internal Logic:**
  - Password Hashing: `bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)`.
  - Sessions: Persisted with random `refresh_jti`. Revocation sets `revoked_at = NOW()`.
  - PAT Verification (`POST /internal/pats/verify`): Computes SHA-256 of incoming raw token, queries `personal_access_tokens` where `token_hash = $1 AND revoked_at IS NULL`, updates `last_used_at = NOW()`, returns associated user claims.

### 6.2 organization (:8002)
- **Port:** `8002`
- **Tables Owned:** `organizations`, `org_members`, `org_invites`
- **Internal Logic:**
  - Slugs: Enforces DNS-safe regex `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`.
  - Member Lookup (`GET /internal/orgs/{orgId}/members/{userId}`): Single SQL query returning role string. Used by gateway for every org request.
  - Invites: Mints random token `inv_...` expiring in 7 days. Emits `org.invite_created` on `TopicOrg`.

### 6.3 project (:8003)
- **Port:** `8003`
- **Tables Owned:** `projects`
- **Internal Logic:**
  - Desired State (`PUT .../config`): Validates manifest against JSON schema, calculates SHA-256 hash of canonical representation, stores `jp_config_yaml` and `jp_config_hash`.
  - Drift Detection (`GET .../config/drift`): Compares stored hash against active runtime container configuration and domain routing state.

### 6.4 notification (:8004)
- **Port:** `8004`
- **Tables Owned:** None (Stateless)
- **Internal Logic:**
  - Receives email delivery commands.
  - In `dev` profile, connects via SMTP to `mailhog:1025` to deliver HTML verification and invitation emails.

### 6.5 repository (:8005)
- **Port:** `8005`
- **Tables Owned:** `github_installations`, `connected_repos`
- **Internal Logic:**
  - **GitHub App Handshake:** When configured with `GITHUB_APP_ID` and RSA Private Key, mints RS256 JWTs to exchange for GitHub App Installation Tokens.
  - **Webhook Ingestion (`POST /webhooks/github`):** Validates HMAC-SHA256 signature using `GITHUB_WEBHOOK_SECRET`. Handles `push` events by triggering deployment on matching branches; handles `pull_request` by spinning up ephemeral preview environments.
  - **Commit Statuses:** Calls GitHub REST API to post pending/success/failure commit check statuses.

### 6.6 deployment (:8006)
- **Port:** `8006`
- **Tables Owned:** `deployments`
- **Internal Logic:**
  - State Machine: `queued` → `building` → `ready` \| `failed`.
  - Creates deployment record, calls `build:8007` to enqueue image compilation, publishes `deploy.created` to `jp.deploy`.
  - Rollback Engine: Finds previous successful deployment record, mints new deployment referencing previous `image_ref`, sets `rollback_of = prior_deployment_id`, notifies scheduler immediately.

### 6.7 build (:8007)
- **Port:** `8007`
- **Tables Owned:** `builds`
- **Internal Logic:**
  - Creates build record with status `queued`.
  - Publishes `build.queued` event to Redis Stream `jp.build`.
  - Provides log streaming endpoint: reads accumulated log buffer from database.

### 6.8 worker (:8008)
- **Port:** `8008`
- **Tables Owned:** None (Worker daemon)
- **Internal Logic:**
  - Subscribed to `jp.build` via consumer group `jp-workers`.
  - Limits simultaneous builds using a worker semaphore sized to `WORKER_CONCURRENCY`.
  - **Docker BuildKit Engine:** Clones source code Git repository to temporary workspace `/tmp/jp-builds/{build_id}`, executes `docker buildx build --load -t host.docker.internal:5000/{proj}:{build_id} .`, pushes image to `registry:5000`, updates `build` service status to `succeeded`, updates `deployment` service status to `ready`.

### 6.9 registry-api (:8009) & registry:2 (:5000)
- **Port:** `8009` (API) & `5000` (OCI Registry)
- **Tables Owned:** `images`
- **Internal Logic:**
  - `registry:2` runs upstream open-source Docker Registry daemon.
  - `registry-api` catalogs tags, image layer sizes, digests, and project associations.

### 6.10 runtime (:8010)
- **Port:** `8010`
- **Tables Owned:** `runtime_instances`
- **Internal Logic:**
  - Controls Docker Engine via `/var/run/docker.sock`.
  - Starts containers with network attached to bridge `jp`, memory/CPU constraints applied, environment variables injected, and restart policies configured.
  - Fallback: If Docker daemon is unavailable, operates in `simulate` mode generating `sim-*` instances.

### 6.11 scheduler (:8011)
- **Port:** `8011`
- **Tables Owned:** None
- **Internal Logic:**
  - Subscribed to `jp.deploy` on consumer group `jp-scheduler`.
  - Listens for deployments marked `ready`.
  - Triggers rolling update or blue/green traffic switch.
  - Cleanup Cron: Background loop checks for preview deployments older than `PREVIEW_TTL=72h` and orphan container images older than `ORPHAN_IMAGE_TTL=168h` and issues deprovisioning commands.

### 6.12 domain (:8012)
- **Port:** `8012`
- **Tables Owned:** `domains`
- **Internal Logic:**
  - DNS Verification: Resolves CNAME records pointing to `DOMAIN_CNAME_TARGET` or TXT verification records.
  - Dynamic Ingress: Generates Traefik dynamic router YAML files into `/etc/traefik/dynamic/domain-{id}.yml`.

### 6.13 certificate (:8013)
- **Port:** `8013`
- **Tables Owned:** `certificates`
- **Internal Logic:**
  - Manages SSL/TLS certificate lifecycles.
  - Interacts with Traefik ACME resolver or generates synthetic TLS certificate metadata when `CERT_SIMULATE=true`.

### 6.14 secret (:8014)
- **Port:** `8014`
- **Tables Owned:** `project_environments`, `secrets`, `secret_versions`, `secret_audit`
- **Internal Logic:**
  - AES-256-GCM Envelope Encryption.
  - Generates 12-byte random nonce per secret version.
  - Plaintext values are never returned across the API. API responses return masked hints (`value_hint`).

### 6.15 logging (:8015)
- **Port:** `8015`
- **Tables Owned:** `log_entries`
- **Internal Logic:**
  - Ingests structured log lines via `POST .../logs/ingest`.
  - Merges build log output with runtime log output upon query.
  - Forwards log streams to Grafana Loki (`LOKI_URL=http://loki:3100`) when monitoring profile is active.

### 6.16 metrics (:8016)
- **Port:** `8016`
- **Tables Owned:** `metric_samples`, `scrape_targets`
- **Internal Logic:**
  - Exposes Prometheus `/metrics` endpoint.
  - Calculates aggregated rollups for CPU, RAM, request volume, and latency.

### 6.17 storage (:8017)
- **Port:** `8017`
- **Tables Owned:** `storage_buckets`, `storage_objects`
- **Internal Logic:**
  - Connects to MinIO S3 API on port `9000` (`minio-go/v7`).
  - Automatically provisions project bucket `jp-proj-{project_id}`.
  - Supports base64 payload upload and pre-signed download/upload URL generation.

### 6.18 database (:8018)
- **Port:** `8018`
- **Tables Owned:** `managed_databases`
- **Internal Logic:**
  - One-click managed PostgreSQL database provisioner (`DB_MODE=schema`).
  - Provisions schema `app_{db_id}`, creates an isolated database user role, generates a random 24-character password, and stores the connection URL in `secret:8014`.

### 6.19 addon (:8021)
- **Port:** `8021`
- **Tables Owned:** `managed_addons`
- **Internal Logic:**
  - Marketplace provisioner for backing stores: PostgreSQL, MySQL, MongoDB, Redis, RabbitMQ, Kafka, SQLite.
  - In `shared` mode, connects to Compose `addons` profile broker containers to provision schemas, databases, and vhosts, saving credentials directly into project secrets.

### 6.20 ai (:8019)
- **Port:** `8019`
- **Tables Owned:** None
- **Internal Logic:**
  - Incident diagnosis assistant (`POST .../ai/explain`).
  - Tool-Assisted Retrieval: Queries `build:8007` for build logs, `logging:8015` for runtime error logs, and `deployment:8006` for recent deployment events.
  - Evaluates root causes using OpenAI API (`gpt-4o-mini`) or local rule-based heuristic analyzer.

### 6.21 billing (:8020)
- **Port:** `8020`
- **Tables Owned:** `billing_usage_events`, `billing_org_plans`
- **Internal Logic:**
  - Defines subscription tiers: `free`, `pro`, `scale`.
  - Consumes `jp.build` and `jp.deploy` streams on consumer group `jp-billing` to calculate compute usage.

### 6.22 event (packages/events)
- **Port:** None (Core Go library)
- **Internal Logic:** Centralized definitions for Redis Streams envelope, consumer group helpers, topic constants, and serialization routines.

---

## 7. Database Schemas, DDL & Migration Engine

### 7.1 Entity Relationship Diagram (ERD)

```mermaid
erDiagram
  users ||--o{ sessions : "owns"
  users ||--o{ personal_access_tokens : "creates"
  users ||--o{ org_members : "participates"
  users ||--o{ audit_logs : "triggers"

  organizations ||--o{ org_members : "contains"
  organizations ||--o{ org_invites : "issues"
  organizations ||--o{ projects : "owns"
  organizations ||--o{ github_installations : "connects"
  organizations ||--o{ billing_org_plans : "maintains"

  projects ||--o{ connected_repos : "links"
  projects ||--o{ deployments : "executes"
  projects ||--o{ builds : "triggers"
  projects ||--o{ images : "produces"
  projects ||--o{ runtime_instances : "hosts"
  projects ||--o{ domains : "maps"
  projects ||--o{ certificates : "secures"
  projects ||--o{ secrets : "configures"
  projects ||--o{ storage_buckets : "owns"
  projects ||--o{ managed_databases : "provisions"
  projects ||--o{ managed_addons : "provisions"
  projects ||--o{ log_entries : "emits"

  secrets ||--o{ secret_versions : "versions"
  storage_buckets ||--o{ storage_objects : "stores"
```

### 7.2 Migration File Ordering & Execution Engine

Each service runs migrations on startup via `postgres.Migrate()`. Migrations are stored under `services/<service>/migrations/` and follow the strict naming convention `<service>_00X_<description>.sql`.

### 7.3 Complete DDL for Every Service Table

```sql
-- 1. Identity Service (services/identity/migrations/identity_001_init.sql)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_jti TEXT NOT NULL UNIQUE,
    user_agent TEXT,
    ip TEXT,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE personal_access_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    scopes TEXT[] NOT NULL DEFAULT '{}',
    last_used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_pats_user ON personal_access_tokens(user_id);

CREATE TABLE audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id UUID,
    action TEXT NOT NULL,
    resource TEXT NOT NULL,
    resource_id TEXT,
    org_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}',
    ip TEXT,
    user_agent TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_audit_logs_actor ON audit_logs(actor_id);
CREATE INDEX idx_audit_logs_created ON audit_logs(created_at DESC);

-- 2. Organization Service (services/organization/migrations/organization_001_init.sql)
CREATE TABLE organizations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    slug TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE org_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, user_id)
);
CREATE INDEX idx_org_members_user ON org_members(user_id);

CREATE TABLE org_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    token TEXT NOT NULL UNIQUE,
    invited_by UUID,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_org_invites_lookup ON org_invites(org_id, email);

-- 3. Project Service (services/project/migrations/project_001_init.sql & 002_jp_config.sql)
CREATE TABLE projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    jp_config_yaml TEXT,
    jp_config_hash TEXT,
    jp_config_applied_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, slug)
);
CREATE INDEX idx_projects_org ON projects(org_id);

-- 4. Repository Service (services/repository/migrations/repository_001_init.sql)
CREATE TABLE github_installations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    installation_id BIGINT NOT NULL,
    account_login TEXT NOT NULL,
    account_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, installation_id)
);

CREATE TABLE connected_repos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    installation_id BIGINT,
    full_name TEXT NOT NULL,
    default_branch TEXT NOT NULL DEFAULT 'main',
    clone_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, full_name)
);
CREATE INDEX idx_connected_repos_project ON connected_repos(org_id, project_id);

-- 5. Deployment Service (services/deployment/migrations/deployment_001_init.sql & 002_strategy.sql)
CREATE TABLE deployments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    source TEXT NOT NULL DEFAULT 'manual',
    git_sha TEXT NOT NULL DEFAULT '',
    git_branch TEXT NOT NULL DEFAULT 'main',
    clone_url TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'building', 'ready', 'failed')),
    image_ref TEXT NOT NULL DEFAULT '',
    build_id UUID,
    strategy TEXT NOT NULL DEFAULT 'rolling' CHECK (strategy IN ('rolling', 'blue_green')),
    rollback_of UUID,
    message TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_deployments_project ON deployments(org_id, project_id, created_at DESC);

-- 6. Build Service (services/build/migrations/build_001_init.sql)
CREATE TABLE builds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    framework TEXT NOT NULL DEFAULT 'dockerfile',
    git_sha TEXT NOT NULL DEFAULT '',
    image_ref TEXT NOT NULL DEFAULT '',
    logs TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_builds_project ON builds(org_id, project_id, created_at DESC);

-- 7. Registry Service (services/registry/migrations/registry_001_init.sql)
CREATE TABLE images (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    tag TEXT NOT NULL,
    digest TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, tag)
);

-- 8. Runtime Service (services/runtime/migrations/runtime_001_init.sql)
CREATE TABLE runtime_instances (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    deployment_id UUID,
    kind TEXT NOT NULL DEFAULT 'container',
    image_ref TEXT NOT NULL,
    container_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    desired_state TEXT NOT NULL DEFAULT 'running',
    mode TEXT NOT NULL DEFAULT 'docker',
    health_status TEXT NOT NULL DEFAULT 'healthy',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 9. Domain Service (services/domain/migrations/domain_001_init.sql)
CREATE TABLE domains (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    hostname TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'verified', 'failed')),
    verification_type TEXT NOT NULL DEFAULT 'cname',
    verification_token TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    certificate_id UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 10. Certificate Service (services/certificate/migrations/certificate_001_init.sql)
CREATE TABLE certificates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    hostname TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'issued',
    provider TEXT NOT NULL DEFAULT 'letsencrypt',
    expires_at TIMESTAMPTZ NOT NULL,
    renewed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 11. Secret Service (services/secret/migrations/secret_001_init.sql)
CREATE TABLE project_environments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    name TEXT NOT NULL CHECK (name IN ('development', 'preview', 'staging', 'production')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, name)
);

CREATE TABLE secrets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    environment TEXT NOT NULL,
    name TEXT NOT NULL,
    current_version INT NOT NULL DEFAULT 1,
    value_hint TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, environment, name)
);

CREATE TABLE secret_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    secret_id UUID NOT NULL REFERENCES secrets(id) ON DELETE CASCADE,
    version INT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    key_version INT NOT NULL DEFAULT 1,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (secret_id, version)
);

CREATE TABLE secret_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    environment TEXT NOT NULL DEFAULT '',
    secret_name TEXT NOT NULL DEFAULT '',
    action TEXT NOT NULL,
    actor_id UUID,
    metadata JSONB NOT NULL DEFAULT '{}',
    request_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 12. Logging Service (services/logging/migrations/logging_001_init.sql)
CREATE TABLE log_entries (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    source TEXT NOT NULL DEFAULT 'runtime',
    level TEXT NOT NULL DEFAULT 'info',
    message TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_log_entries_query ON log_entries(project_id, source, timestamp DESC);

-- 13. Metrics Service (services/metrics/migrations/metrics_001_init.sql)
CREATE TABLE metric_samples (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    metric_name TEXT NOT NULL,
    value DOUBLE PRECISION NOT NULL,
    labels JSONB NOT NULL DEFAULT '{}',
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE scrape_targets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    job_name TEXT NOT NULL,
    endpoint TEXT NOT NULL,
    interval_seconds INT NOT NULL DEFAULT 15,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 14. Storage Service (services/storage/migrations/storage_001_init.sql)
CREATE TABLE storage_buckets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL UNIQUE,
    bucket_name TEXT NOT NULL UNIQUE,
    region TEXT NOT NULL DEFAULT 'us-east-1',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE storage_objects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    bucket_name TEXT NOT NULL,
    key TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    content_type TEXT NOT NULL DEFAULT 'application/octet-stream',
    etag TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, key)
);

-- 15. Database Service (services/database/migrations/database_001_init.sql)
CREATE TABLE managed_databases (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    name TEXT NOT NULL,
    engine TEXT NOT NULL DEFAULT 'postgres',
    version TEXT NOT NULL DEFAULT '16',
    status TEXT NOT NULL DEFAULT 'ready',
    mode TEXT NOT NULL DEFAULT 'schema',
    db_name TEXT NOT NULL,
    db_user TEXT NOT NULL,
    secret_ref TEXT NOT NULL,
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id, name)
);

-- 16. Add-on Service (services/addon/migrations/addon_001_init.sql)
CREATE TABLE managed_addons (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    engine TEXT NOT NULL,
    name TEXT NOT NULL,
    mode TEXT NOT NULL DEFAULT 'simulate',
    status TEXT NOT NULL DEFAULT 'ready',
    endpoint TEXT NOT NULL DEFAULT '',
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    secret_ref TEXT NOT NULL DEFAULT '',
    connection_hint TEXT NOT NULL DEFAULT '',
    created_by UUID,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (org_id, project_id, engine, name)
);
CREATE INDEX idx_managed_addons_project ON managed_addons (org_id, project_id);
CREATE INDEX idx_managed_addons_engine ON managed_addons (org_id, project_id, engine);

-- 17. Billing Service (services/billing/migrations/billing_001_init.sql)
CREATE TABLE billing_org_plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL UNIQUE,
    plan_id TEXT NOT NULL DEFAULT 'free',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE billing_usage_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID,
    event_type TEXT NOT NULL,
    quantity BIGINT NOT NULL DEFAULT 1,
    metadata JSONB NOT NULL DEFAULT '{}',
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

---

## 8. Exhaustive API Route & Request/Response Specification

All routes are mounted at `/api/v1` through the gateway. Bearer token auth is required unless explicitly noted as **[PUBLIC]**.

### 8.1 Auth & PAT API
- `POST /auth/register` **[PUBLIC]**
  - **Body:** `{"email": "string", "password": "string(>=8)", "name": "string"}`
  - **Response 201:** `{"access_token": "string", "refresh_token": "string", "user": {"id": "uuid", "email": "string", "name": "string"}}`
- `POST /auth/login` **[PUBLIC]**
  - **Body:** `{"email": "string", "password": "string"}`
  - **Response 200:** Same as register.
- `POST /auth/refresh` **[PUBLIC]**
  - **Body:** `{"refresh_token": "string"}`
  - **Response 200:** `{"access_token": "string", "refresh_token": "string"}`
- `POST /auth/logout`
  - **Response 204:** No Content.
- `GET /auth/me`
  - **Response 200:** `{"user": {"id": "uuid", "email": "string", "name": "string"}}`
- `GET /auth/sessions`
  - **Response 200:** `{"sessions": [{"id": "uuid", "user_agent": "string", "ip": "string", "created_at": "timestamp", "expires_at": "timestamp"}]}`
- `DELETE /auth/sessions/{id}`
  - **Response 204:** No Content.
- `GET /auth/pats`
  - **Response 200:** `{"pats": [{"id": "uuid", "name": "string", "token_prefix": "string", "scopes": ["*"], "last_used_at": "timestamp"}]}`
- `POST /auth/pats`
  - **Body:** `{"name": "string", "scopes": ["string"]}`
  - **Response 201:** `{"id": "uuid", "name": "string", "token": "jp_pat_...", "scopes": ["*"]}` *(Token shown once)*
- `DELETE /auth/pats/{id}`
  - **Response 204:** No Content.

### 8.2 Organization & Team RBAC API
- `GET /orgs`
  - **Response 200:** `{"orgs": [{"id": "uuid", "name": "string", "slug": "string", "role": "owner"}]}`
- `POST /orgs`
  - **Body:** `{"name": "string", "slug": "string(optional)"}`
  - **Response 201:** `{"org": {"id": "uuid", "name": "string", "slug": "string"}}`
- `GET /orgs/{orgId}`
  - **Response 200:** `{"org": {"id": "uuid", "name": "string", "slug": "string", "role": "string"}}`
- `POST /orgs/{orgId}/invites`
  - **Body:** `{"email": "string", "role": "member|admin|viewer"}`
  - **Response 201:** `{"invite": {"id": "uuid", "email": "string", "role": "string", "expires_at": "timestamp"}}`
- `POST /orgs/invites/accept`
  - **Body:** `{"token": "string"}`
  - **Response 200:** `{"org": {"id": "uuid", "name": "string", "slug": "string"}}`
- `GET /orgs/{orgId}/members`
  - **Response 200:** `{"members": [{"user_id": "uuid", "email": "string", "name": "string", "role": "string", "joined_at": "timestamp"}]}`

### 8.3 Project & IaC API
- `GET /orgs/{orgId}/projects`
  - **Response 200:** `{"projects": [{"id": "uuid", "name": "string", "slug": "string", "description": "string"}]}`
- `POST /orgs/{orgId}/projects`
  - **Body:** `{"name": "string", "slug": "string", "description": "string"}`
  - **Response 201:** `{"project": {"id": "uuid", "name": "string", "slug": "string"}}`
- `GET /orgs/{orgId}/projects/{projectId}`
  - **Response 200:** `{"project": {...}}`
- `PATCH /orgs/{orgId}/projects/{projectId}`
  - **Body:** `{"name": "string", "description": "string"}`
  - **Response 200:** `{"project": {...}}`
- `DELETE /orgs/{orgId}/projects/{projectId}`
  - **Response 204:** No Content.
- `PUT /orgs/{orgId}/projects/{projectId}/config`
  - **Body:** `{"raw": "yaml string", "manifest": {...}}`
  - **Response 200:** `{"applied": true, "hash": "sha256...", "applied_at": "timestamp"}`
- `GET /orgs/{orgId}/projects/{projectId}/config`
  - **Response 200:** `{"yaml": "string", "hash": "string", "applied_at": "timestamp"}`
- `GET /orgs/{orgId}/projects/{projectId}/config/drift`
  - **Response 200:** `{"has_drift": false, "expected_hash": "...", "current_hash": "..."}`

### 8.4 Git & Webhook Receiver API
- `POST /orgs/{orgId}/github/install/start`
  - **Response 200:** `{"install_url": "https://github.com/apps/.../installations/new", "state": "uuid"}`
- `POST /orgs/{orgId}/github/install/callback`
  - **Body:** `{"installation_id": "string", "account_login": "string"}`
  - **Response 200:** `{"installation": {"id": "uuid", "account_login": "string"}}`
- `GET /orgs/{orgId}/github/installations`
  - **Response 200:** `{"installations": [...]}`
- `GET /orgs/{orgId}/github/repos`
  - **Response 200:** `{"repos": [{"full_name": "owner/repo", "default_branch": "main", "private": true}], "mode": "real|mock"}`
- `POST /orgs/{orgId}/projects/{projectId}/repos`
  - **Body:** `{"full_name": "owner/repo", "clone_url": "...", "default_branch": "main", "installation_id": 12345}`
  - **Response 201:** `{"repo": {"id": "uuid", "full_name": "owner/repo"}}`
- `GET /orgs/{orgId}/projects/{projectId}/repos`
  - **Response 200:** `{"repos": [...]}`
- `DELETE /orgs/{orgId}/projects/{projectId}/repos/{repoId}`
  - **Response 204:** No Content.
- `POST /webhooks/github` **[PUBLIC]**
  - **Headers:** `X-Hub-Signature-256: sha256=...`, `X-GitHub-Event: push|pull_request`
  - **Response 200:** `{"ok": true}`

### 8.5 Deployment & Rollback API
- `GET /orgs/{orgId}/projects/{projectId}/deployments`
  - **Response 200:** `{"deployments": [{"id": "uuid", "status": "queued|building|ready|failed", "git_sha": "string", "git_branch": "string", "strategy": "rolling|blue_green", "created_at": "timestamp"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/deployments`
  - **Body:** `{"git_sha": "string", "git_branch": "string", "strategy": "rolling", "message": "string"}`
  - **Response 201:** `{"deployment": {"id": "uuid", "status": "queued", "build_id": "uuid"}}`
- `GET /orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}`
  - **Response 200:** `{"deployment": {...}}`
- `POST /orgs/{orgId}/projects/{projectId}/deployments/rollback`
  - **Response 200:** `{"deployment": {"id": "uuid", "status": "ready", "rollback_of": "uuid"}}`
- `POST /orgs/{orgId}/projects/{projectId}/deployments/{deploymentId}/rollback`
  - **Response 200:** Same as above.

### 8.6 Build & Registry API
- `GET /orgs/{orgId}/projects/{projectId}/builds`
  - **Response 200:** `{"builds": [{"id": "uuid", "status": "succeeded", "framework": "dockerfile", "git_sha": "string", "image_ref": "string"}]}`
- `GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}`
  - **Response 200:** `{"build": {...}}`
- `GET /orgs/{orgId}/projects/{projectId}/builds/{buildId}/logs`
  - **Response 200:** `{"build_id": "uuid", "status": "succeeded", "logs": "Step 1/5 : FROM node:20-alpine\n..."}`
- `GET /orgs/{orgId}/projects/{projectId}/images`
  - **Response 200:** `{"images": [{"id": "uuid", "tag": "string", "digest": "sha256...", "size_bytes": 12345678}]}`

### 8.7 Runtime & Container API
- `GET /orgs/{orgId}/projects/{projectId}/runtime/instances`
  - **Response 200:** `{"instances": [{"id": "uuid", "container_id": "string", "image_ref": "string", "status": "running", "health_status": "healthy"}], "mode": "docker|simulate"}`
- `POST /orgs/{orgId}/projects/{projectId}/runtime/instances`
  - **Body:** `{"image_ref": "string", "kind": "container"}`
  - **Response 201:** `{"instance": {...}}`
- `POST /orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/start`
  - **Response 200:** `{"instance": {"status": "running"}}`
- `POST /orgs/{orgId}/projects/{projectId}/runtime/instances/{instanceId}/stop`
  - **Response 200:** `{"instance": {"status": "stopped"}}`
- `GET /orgs/{orgId}/projects/{projectId}/runtime/containers`
  - **Response 200:** `{"containers": [{"id": "string", "names": ["/app"], "state": "running"}]}`

### 8.8 Domain & Ingress Routing API
- `GET /orgs/{orgId}/projects/{projectId}/domains`
  - **Response 200:** `{"domains": [{"id": "uuid", "hostname": "app.example.com", "status": "verified|pending", "verification_token": "token"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/domains`
  - **Body:** `{"hostname": "app.example.com"}`
  - **Response 201:** `{"domain": {"id": "uuid", "hostname": "app.example.com", "status": "pending", "verification_token": "..."}}`
- `POST /orgs/{orgId}/projects/{projectId}/domains/{domainId}/verify`
  - **Body:** `{"force": false}`
  - **Response 200:** `{"domain": {"id": "uuid", "status": "verified"}, "verified": true}`
- `DELETE /orgs/{orgId}/projects/{projectId}/domains/{domainId}`
  - **Response 204:** No Content.

### 8.9 TLS Certificate API
- `GET /orgs/{orgId}/projects/{projectId}/certificates`
  - **Response 200:** `{"certificates": [{"id": "uuid", "hostname": "app.example.com", "status": "issued", "expires_at": "timestamp"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/certificates/{certId}/renew`
  - **Response 200:** `{"certificate": {"status": "issued", "renewed_at": "timestamp"}}`

### 8.10 Environments & Encrypted Secrets API
- `GET /orgs/{orgId}/projects/{projectId}/environments`
  - **Response 200:** `{"environments": [{"id": "uuid", "name": "development|preview|staging|production"}]}`
- `GET /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets`
  - **Response 200:** `{"secrets": [{"id": "uuid", "name": "API_KEY", "current_version": 2, "value_hint": "sk_l...4a9f", "updated_at": "timestamp"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets`
  - **Body:** `{"name": "API_KEY", "value": "sk_live_1234567890abcdef"}`
  - **Response 201:** `{"secret": {"name": "API_KEY", "value_hint": "sk_l...cdef"}}`
- `PUT /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}`
  - **Body:** `{"value": "new_secret_value"}`
  - **Response 200:** `{"secret": {...}}`
- `DELETE /orgs/{orgId}/projects/{projectId}/environments/{env}/secrets/{name}`
  - **Response 204:** No Content.

### 8.11 Telemetry Logging API
- `GET /orgs/{orgId}/projects/{projectId}/logs`
  - **Query:** `?source=runtime|build&limit=100&q=keyword`
  - **Response 200:** `{"entries": [{"timestamp": "...", "source": "runtime", "level": "info", "message": "Listening on port 3000"}], "backend": "loki|postgres"}`
- `POST /orgs/{orgId}/projects/{projectId}/logs`
  - **Body:** `{"source": "runtime", "level": "info", "message": "string"}`
  - **Response 200:** `{"ingested": 1}`
- `POST /orgs/{orgId}/projects/{projectId}/logs/ingest`
  - **Body:** `{"entries": [...]}`
  - **Response 200:** `{"ingested": 50}`

### 8.12 Metrics & Prometheus API
- `GET /orgs/{orgId}/projects/{projectId}/metrics`
  - **Response 200:** `{"metrics": [{"metric": "cpu_usage_percent", "value": 14.5}, {"metric": "memory_usage_bytes", "value": 128000000}], "mode": "live|simulate", "prometheus_url": "http://prometheus:9090"}`
- `GET /orgs/{orgId}/projects/{projectId}/metrics/targets`
  - **Response 200:** `{"targets": [{"job_name": "app", "endpoint": "http://..."}]}`

### 8.13 MinIO S3 Storage API
- `GET /orgs/{orgId}/projects/{projectId}/storage/bucket`
  - **Response 200:** `{"bucket": {"bucket_name": "jp-proj-uuid", "region": "us-east-1"}, "mode": "minio|simulate"}`
- `GET /orgs/{orgId}/projects/{projectId}/storage/objects`
  - **Query:** `?prefix=uploads/`
  - **Response 200:** `{"objects": [{"key": "uploads/doc.pdf", "size_bytes": 10240, "content_type": "application/pdf", "created_at": "timestamp"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/storage/objects`
  - **Body:** `{"key": "notes.txt", "data_base64": "aGVsbG8gd29ybGQ=", "content_type": "text/plain"}`
  - **Response 201:** `{"object": {"key": "notes.txt", "size_bytes": 11}}`
- `POST /orgs/{orgId}/projects/{projectId}/storage/signed-url`
  - **Body:** `{"key": "notes.txt", "expires": "15m"}`
  - **Response 200:** `{"url": "http://localhost:9000/jp-proj-uuid/notes.txt?X-Amz-Signature=...", "expires_in_seconds": 900}`
- `DELETE /orgs/{orgId}/projects/{projectId}/storage/objects`
  - **Query:** `?key=notes.txt`
  - **Response 204:** No Content.

### 8.14 Managed Relational Database API
- `GET /orgs/{orgId}/projects/{projectId}/databases`
  - **Response 200:** `{"databases": [{"id": "uuid", "name": "app_db", "engine": "postgres", "status": "ready", "secret_ref": "DATABASE_URL"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/databases`
  - **Body:** `{"name": "app_db", "env": "development"}`
  - **Response 201:** `{"database": {"id": "uuid", "name": "app_db", "secret_ref": "DATABASE_URL"}}`
- `DELETE /orgs/{orgId}/projects/{projectId}/databases/{dbId}`
  - **Response 204:** No Content.

### 8.15 Backing Add-ons Marketplace API
- `GET /orgs/{orgId}/projects/{projectId}/addons/catalog`
  - **Response 200:** `{"catalog": [{"id": "redis", "name": "Redis", "category": "cache", "description": "In-memory data store"}, {"id": "mysql", "name": "MySQL", "category": "database"}]}`
- `GET /orgs/{orgId}/projects/{projectId}/addons`
  - **Response 200:** `{"addons": [{"id": "uuid", "engine": "redis", "name": "cache", "status": "ready", "secret_ref": "REDIS_URL"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/addons`
  - **Body:** `{"engine": "redis", "name": "cache", "env": "development"}`
  - **Response 201:** `{"addon": {"id": "uuid", "engine": "redis", "name": "cache", "secret_ref": "REDIS_URL"}}`
- `DELETE /orgs/{orgId}/projects/{projectId}/addons/{addonId}`
  - **Response 204:** No Content.

### 8.16 Cron Schedules & Platform Queue API
- `GET /orgs/{orgId}/projects/{projectId}/cron`
  - **Response 200:** `{"schedules": [{"id": "uuid", "schedule": "@hourly", "command": "npm run task"}]}`
- `POST /orgs/{orgId}/projects/{projectId}/cron`
  - **Body:** `{"schedule": "@daily", "command": "npm run cleanup"}`
  - **Response 201:** `{"schedule": {"id": "uuid"}}`
- `DELETE /orgs/{orgId}/projects/{projectId}/cron/{cronId}`
  - **Response 204:** No Content.
- `GET /orgs/{orgId}/projects/{projectId}/queues`
  - **Response 200:** `{"queues": [{"name": "jp.build", "length": 0, "consumers": 2}]}`

### 8.17 AI Ops Diagnostic API
- `POST /orgs/{orgId}/projects/{projectId}/ai/explain`
  - **Body:** `{"deployment_id": "uuid", "build_id": "uuid", "prompt": "Why did build fail?"}`
  - **Response 200:** `{"explanation": "BuildKit failed at Step 3/5: Module 'react-dom' not found. Run npm install react-dom.", "mode": "openai|heuristic"}`
- `POST /orgs/{orgId}/ai/ask`
  - **Body:** `{"prompt": "How do I configure rolling deployment?", "project_id": "uuid"}`
  - **Response 200:** `{"answer": "Set deploy.strategy: rolling in your jp.yaml manifest...", "mode": "openai|heuristic"}`
- `GET /orgs/{orgId}/ai/status`
  - **Response 200:** `{"mode": "openai", "model": "gpt-4o-mini", "tools": ["fetch_build_logs", "fetch_runtime_logs", "fetch_deployments"]}`

### 8.18 Billing & Metering API
- `GET /billing/plans`
  - **Response 200:** `{"plans": [{"id": "free", "name": "Free", "price_monthly": 0}, {"id": "pro", "name": "Pro", "price_monthly": 29}, {"id": "scale", "name": "Scale", "price_monthly": 99}]}`
- `GET /orgs/{orgId}/billing/usage`
  - **Response 200:** `{"plan": {"id": "pro"}, "usage": [{"metric": "build_minutes", "used": 142, "limit": 1000}, {"metric": "runtime_hours", "used": 480, "limit": 2000}]}`
- `PUT /orgs/{orgId}/billing/plan`
  - **Body:** `{"plan_id": "scale"}`
  - **Response 200:** `{"plan": {"id": "scale"}}`

---

## 9. CLI Architecture & Command Implementation (`apps/cli`)

### 9.1 CLI Anatomy & Configuration Storage

- **Framework:** Cobra (`github.com/spf13/cobra`).
- **Config Storage:** Saved to `~/.jp/config.json`.
- **Config Schema:**
  ```json
  {
    "api_url": "http://localhost:8000/api/v1",
    "access_token": "eyJhbGciOiJIUzI1Ni...",
    "refresh_token": "eyJhbGciOiJIUzI1Ni...",
    "current_org_id": "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
  }
  ```

### 9.2 Command Tree, Flag Bindings & Pre-Run Validations

```
jp
├── login [--token]
├── logout
├── whoami
├── orgs
├── org use <slug|id>
├── projects
├── init [name]
├── apply [drift]
├── drift
├── deploy --project <id> [--branch] [--sha] [--strategy]
├── status --project <id>
├── builds --project <id>
├── rollback [id] --project <id>
├── domains (list | add | verify | delete) --project <id>
├── secrets (list | set | unset) --project <id> [--env]
├── env (list | set | unset) --project <id> [--env]
├── logs --project <id> [--source] [--build] [--limit]
├── metrics --project <id>
├── storage (bucket | ls | put | sign | rm) --project <id>
├── db (list | create | delete) --project <id>
├── addon (catalog | list | create | delete) --project <id>
├── ai "<prompt>" --project <id>
├── config (get | set)
└── trace
```

#### Shared Pre-Run Guards:
Every command requiring authentication runs:
```go
client, cfg, err := newClient()
if err != nil { return err }
if err := requireLogin(cfg); err != nil { return err }
if cfg.CurrentOrgID == "" {
    return fmt.Errorf("no organization selected; run `jp org use <slug|id>`")
}
```

### 9.3 HTTP API Client Internals (`internal/client`)

Located at `apps/cli/internal/client/client.go`.
- Wraps standard `http.Client`.
- Injects `Authorization: Bearer <token>`.
- Auto-deserializes JSON error bodies into clean Go errors using `httpx.ErrorDetail`.
- Helper methods mirror the Gateway API: `client.CreateDeployment()`, `client.ListBuilds()`, `client.SetSecret()`, `client.VerifyDomain()`.

### 9.4 Local Template Generators & YAML Scaffolding

Implemented in `apps/cli/cmd/init.go`:
- Inspects current working directory for:
  - `package.json` → Sets `runtime: nodejs`
  - `requirements.txt` or `pyproject.toml` → Sets `runtime: python`
  - `go.mod` → Sets `runtime: go`
  - `Dockerfile` → Sets `runtime: docker`
- Emits formatted `jp.yaml` adhering to `jp.schema.json`.

---

## 10. Dashboard Web Console Architecture (`apps/dashboard`)

### 10.1 Next.js 16 App Router Structure & Layouts

- **Next.js Version:** 16.2 with React 19 and Tailwind CSS 4.
- **Route Groups:** `app/(console)/` wraps all authenticated views with a single shared layout rendering `AppShell` (`components/app-shell.tsx`).
- **Auth Guard:** Every console page is wrapped in `<RequireAuth>` (`components/require-auth.tsx`). If unauthenticated, immediately redirects to `/login`.

### 10.2 Context Providers & Authentication Life Cycle

1. **`AuthContext` (`src/lib/auth-context.tsx`):**
   - Tracks `user`, `accessToken`, `refreshToken`.
   - Restores session on initial load from `localStorage`.
   - Exposes `login()`, `register()`, `logout()`.
2. **`OrgContext` (`src/lib/org-context.tsx`):**
   - Queries `api.listOrgs()` on load.
   - Restores last-active organization slug from `localStorage`.
   - Exposes `setCurrentOrg(org)`.

### 10.3 API Client with Deadlock-Free Token Refresh

Located at `apps/dashboard/src/lib/api.ts`:
- Maintains a single-flight promise variable: `let refreshPromise: Promise<boolean> | null = null;`.
- When an API request encounters HTTP 401, all concurrent calls await the shared `refreshPromise`.
- The refresh call executes `POST /api/v1/auth/refresh`.
- On success, updates `localStorage` and replays the original requests with the fresh token.
- On failure, calls `clearTokens()` and forces redirect to `/login`.

### 10.4 UI Component Tree & Page Specifications

- **`components/app-shell.tsx`:** Left sidebar navigation + top bar with Organization Selector dropdown and active User Profile menu.
- **`app/(console)/projects/[projectId]/page.tsx`:** Complete project dashboard containing:
  - Active Connected Git Repository widget.
  - Deployment quick-action button with strategy selector modal.
  - Real-time `jp.yaml` desired state viewer with drift indicators.
  - Tabs linking to Deployments, Builds, Runtime, Secrets, Domains, Storage, Databases, Add-ons.
- **`app/(console)/logs/page.tsx`:** High-performance log stream viewer with auto-scroll lock, source filter pill switches (`all`, `runtime`, `build`), and keyword search filter.

---

## 11. Data Plane Execution & Build Farm Pipeline

### 11.1 Worker Engine & BuildKit Pipeline

```mermaid
sequenceDiagram
  autonumber
  participant Redis as Redis Stream (jp.build)
  participant Worker as worker (:8008)
  participant Docker as Docker Daemon (BuildKit)
  participant Registry as registry:2 (:5000)
  participant SvcBuild as build (:8007)
  participant SvcDep as deployment (:8006)

  Worker->>Redis: XREADGROUP jp-workers jp.build >
  Redis-->>Worker: build.queued { build_id, project_id, git_sha, clone_url }
  Worker->>SvcBuild: Update status: running
  Worker->>Worker: Git clone clone_url into /tmp/jp-builds/{build_id}
  Worker->>Docker: Execute BuildKit (Context: /tmp/jp-builds/{build_id})
  Docker-->>Worker: Stream build output lines
  Worker->>SvcBuild: Append build logs
  Docker->>Docker: Tag image host.docker.internal:5000/{proj}:{build_id}
  Worker->>Docker: docker push host.docker.internal:5000/{proj}:{build_id}
  Docker->>Registry: Store OCI layers
  Worker->>SvcBuild: Update status: succeeded, image_ref
  Worker->>SvcDep: Update deployment: status ready, image_ref
  Worker->>Redis: XACK jp.build jp-workers {msg_id}
```

### 11.2 OCI Image Registry Flow (`registry:2` & `registry-api`)

- Image tags follow the deterministic pattern: `host.docker.internal:5000/{project_slug}:{build_id}`.
- Host Docker daemon must include `host.docker.internal:5000` in `insecure-registries` in `daemon.json`.
- `registry-api` registers metadata records into the `images` table upon successful push.

### 11.3 Runtime Engine & Docker Daemon Orchestration

Located at `services/runtime/internal/dockerx/dockerx.go`:
- Communicates directly with the Docker socket `/var/run/docker.sock`.
- Calls:
  - `ContainerCreate()` with container name `jp-{project_slug}-{instance_id}`.
  - Mounts bridge network `jp`.
  - Sets environment variables fetched from `secret` service.
  - Injects labels for Traefik discovery.
  - Calls `ContainerStart()`.
  - Monitors health via periodic container inspection.

### 11.4 Scheduler Loop, Rolling Replaces & Blue/Green Deployments

Located at `services/scheduler/internal/loop/loop.go`:
- Listens to `deploy.updated` events where `status = ready`.
- **Rolling Strategy:**
  1. Issues start command to `runtime` for Version $N$.
  2. Probes container HTTP health check endpoint every 500ms (timeout: 10s).
  3. Upon first healthy response, issues stop command to Version $N-1$ container.
  4. Updates Traefik routing rule.
- **Blue/Green Strategy:**
  1. Launches Version $N$ (Green) alongside Version $N-1$ (Blue).
  2. Updates Traefik weight configuration: 100% Blue, 0% Green.
  3. Executes integration health verification.
  4. Flips weights: 0% Blue, 100% Green.
  5. Keeps Blue alive in standby for rapid rollback before draining.

---

## 12. Envelope Encryption & Security Architecture

### 12.1 AES-256-GCM Envelope Encryption Deep Dive

Located at `services/secret/internal/crypto/envelope.go`.
- Cryptographic primitive: Authenticated Encryption with Associated Data (AEAD) using **AES-256-GCM**.
- Key Material: 32-byte cryptographic key supplied via `SECRETS_MASTER_KEY`.
- Nonce: 12-byte cryptographically secure random bytes generated per encryption via `crypto/rand`.
- Ciphertext: Includes 16-byte GCM authentication tag.

```mermaid
flowchart LR
  Plain["Plaintext Secret (e.g. sk_live_abc)"] --> NonceGen["crypto/rand (12-byte Nonce)"]
  Plain --> AES["AES-256-GCM Encrypt"]
  NonceGen --> AES
  Key["SECRETS_MASTER_KEY (32-byte)"] --> AES

  AES --> CT["Ciphertext + Auth Tag (BYTEA)"]
  NonceGen --> NonceCol["Nonce (BYTEA)"]
  Plain --> HintGen["value_hint Generator (sk_l...4a9f)"]

  CT --> DBVersion[("secret_versions Table")]
  NonceCol --> DBVersion
  HintGen --> DBSecret[("secrets Table (value_hint)")]
```

### 12.2 Nonce Generation & Key Versioning

- A fresh nonce is generated for every update, preventing nonce reuse attacks.
- `key_version` column permits future master key rotation without data loss.
- Version history is append-only in `secret_versions`.

### 12.3 Zero-Plaintext Leak Guarantees

- `GET /environments/{env}/secrets` returns only `value_hint`, `current_version`, `created_at`, `updated_at`.
- Even `GET /environments/{env}/secrets/{name}` returns metadata only.
- Plaintext secrets are decrypted **only** when injected into running runtime containers via the internal `runtime` service network.

---

## 13. Hardware Sizing & Resource Optimization Engine

### 13.1 Host Memory/CPU Detection Engine

Implemented in `scripts/size-host.ps1` and `scripts/size-host.sh`:
- Queries host hardware:
  - Windows: `(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory`
  - Linux: `/proc/meminfo` (`MemTotal`)
- Categorizes hardware into tiers:
  - **`small`:** &lt; 6 GB RAM → 1 Worker, Concurrency 1, Simulate modes enabled.
  - **`medium`:** 6–16 GB RAM → 2 Workers, Concurrency 2, Real Docker builds.
  - **`large`:** 16–32 GB RAM → 4 Workers, Concurrency 2, Monitoring enabled.
  - **`xlarge`:** &gt; 32 GB RAM → 8 Workers, Concurrency 4, Full stack + Add-ons.
- Emits `.env.resources`.

### 13.2 Compose Overlay Architecture

Compose overlays in `infra/compose/sizing/{tier}.yml` dynamically adjust memory and CPU limits:
- `small.yml`: Baseline soft memory limits (Postgres: 512M, Redis: 160M, Worker: 512M).
- `large.yml`: High performance (Postgres: 2GB, Redis: 1GB, Worker: 2GB / 1.5 CPUs).

### 13.3 Worker Concurrency & Worker Scaling

- Scaled horizontally using Docker Compose: `--scale worker=N`.
- Internal worker queue throttled by `WORKER_CONCURRENCY`.

---

## 14. Real vs. Simulate Execution Matrix

Every component supports a toggleable simulation mode to permit testing on low-resource developer machines without running external dependencies:

| Subsystem | Variable | Real Value (`true` / `docker`) | Simulate Value (`simulate` / `false`) |
|---|---|---|---|
| **Build Farm** | `BUILD_MODE` | `docker` (Real BuildKit) | `simulate` (Synthetic log stream) |
| **Runtime** | `RUNTIME_MODE` | `docker` (Real Docker containers) | `simulate` (`sim-*` mock instances) |
| **Domain DNS** | `DOMAIN_DNS_STUB` | `false` (Live public DNS verification) | `true` (Bypasses DNS checks) |
| **TLS Certificates** | `CERT_SIMULATE` | `false` (Traefik ACME challenges) | `true` (Generates synthetic cert records) |
| **Object Storage** | `STORAGE_MODE` | `minio` (Real MinIO S3 API) | `simulate` (Metadata-only in PostgreSQL) |
| **Managed DBs** | `DB_MODE` | `schema` (DDL on shared PostgreSQL) | `simulate` (Synthetic connection strings) |
| **Add-on Marketplace**| `ADDON_MODE` | `shared` (Provisions on Compose brokers) | `simulate` (Synthetic connection strings) |
| **AI Ops** | `OPENAI_API_KEY` | Set with key (Real OpenAI API calls) | Empty (Heuristic exit code parser) |
| **Metrics** | `METRICS_MODE` | `live` (Real container stats) | `simulate` (Generated sinusoidal series) |

---

## 15. Master Environment Variable & Configuration Directory

Complete listing of all environment variables across the monorepo:

| Environment Variable | Default Value | Description |
|---|---|---|
| `JP_ENV` | `development` | Operating environment (`development`, `staging`, `production`). |
| `JP_LOG_LEVEL` | `info` | Minimum log level (`debug`, `info`, `warn`, `error`). |
| `JWT_SECRET` | `change-me-...` | Cryptographic secret for signing and validating JWTs. |
| `JWT_ACCESS_TTL` | `15m` | Access token lifetime duration. |
| `JWT_REFRESH_TTL` | `168h` | Refresh token lifetime duration (7 days). |
| `SECRETS_MASTER_KEY` | *(empty)* | 32-byte hex key for AES-256-GCM secret encryption. |
| `POSTGRES_USER` | `jp` | PostgreSQL database user. |
| `POSTGRES_PASSWORD` | `jp_dev_password` | PostgreSQL database password. |
| `POSTGRES_DB` | `jp` | Platform database name. |
| `DATABASE_URL` | `postgres://jp:jp_dev_password@postgres:5432/jp?sslmode=disable` | Connection string for platform microservices. |
| `REDIS_URL` | `redis://redis:6379/0` | Redis connection URL. |
| `PUBLIC_BASE_URL` | `http://localhost:8000` | Publicly reachable Gateway URL. |
| `CORS_ORIGINS` | `http://localhost:3000,http://localhost:5173` | Allowed CORS origins for web console. |
| `DASHBOARD_URL` | `http://localhost:3000` | Web console URL for auth redirects. |
| `GITHUB_WEBHOOK_SECRET` | `dev-webhook-secret` | HMAC secret to verify GitHub webhook payloads. |
| `GITHUB_APP_ID` | *(empty)* | GitHub App numerical ID. |
| `GITHUB_APP_SLUG` | *(empty)* | GitHub App URL slug. |
| `GITHUB_APP_PRIVATE_KEY` | *(empty)* | PEM-encoded GitHub App private key string. |
| `GITHUB_APP_PRIVATE_KEY_PATH`| *(empty)* | File path to GitHub App private key PEM file. |
| `GITHUB_APP_CLIENT_ID` | *(empty)* | GitHub App OAuth client ID. |
| `GITHUB_APP_CLIENT_SECRET` | *(empty)* | GitHub App OAuth client secret. |
| `BUILD_MODE` | `docker` | Build mode (`docker` vs `simulate`). |
| `RUNTIME_MODE` | `docker` | Runtime mode (`docker` vs `simulate`). |
| `REGISTRY_PUSH_URL` | `host.docker.internal:5000` | Target URL used by Docker daemon to push built images. |
| `REGISTRY_HOST` | `host.docker.internal:5000` | Image hostname prefix stored in metadata. |
| `DOCKER_HOST` | `unix:///var/run/docker.sock` | Path to Docker socket. |
| `WORKER_CONCURRENCY` | `1` | Max simultaneous builds per worker process. |
| `SCHEDULER_SLOT` | `node-1` | Scheduler placement identifier. |
| `CLEANUP_INTERVAL` | `1h` | Interval between background garbage collections. |
| `PREVIEW_TTL` | `72h` | Max lifetime for ephemeral preview environments. |
| `PREVIEW_BASE_DOMAIN` | `preview.jp.localhost` | Wildcard base domain for preview environments. |
| `ORPHAN_IMAGE_TTL` | `168h` | Expiration time before orphan container images are purged. |
| `DOMAIN_DNS_STUB` | `false` | Bypass live DNS resolution checks when true. |
| `DOMAIN_CNAME_TARGET` | `cname.jp.localhost` | Expected CNAME target for custom domains. |
| `CERT_SIMULATE` | `false` | Synthesize TLS certificate records when true. |
| `TRAEFIK_CERT_RESOLVER` | *(empty)* | Traefik ACME resolver name (`letsencrypt`). |
| `STORAGE_MODE` | `minio` | Storage mode (`minio` vs `simulate`). |
| `MINIO_ROOT_USER` | `minioadmin` | MinIO admin user / S3 Access Key. |
| `MINIO_ROOT_PASSWORD` | `minioadmin` | MinIO admin password / S3 Secret Key. |
| `MINIO_ENDPOINT` | `minio:9000` | Internal MinIO API endpoint. |
| `MINIO_PUBLIC_URL` | `http://localhost:9000` | Publicly reachable MinIO API endpoint. |
| `DB_MODE` | `schema` | Database provision mode (`schema` vs `simulate`). |
| `ADDON_MODE` | `simulate` | Add-on mode (`shared` vs `simulate`). |
| `METRICS_MODE` | `live` | Metrics mode (`live` vs `simulate`). |
| `LOKI_URL` | `http://loki:3100` | Grafana Loki URL for log streaming. |
| `PROMETHEUS_URL` | `http://prometheus:9090` | Prometheus URL. |
| `OTEL_EXPORTER_OTLP_ENDPOINT`| `http://tempo:4318` | OpenTelemetry OTLP trace exporter HTTP endpoint. |
| `GRAFANA_ADMIN_USER` | `admin` | Grafana dashboard admin username. |
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Grafana dashboard admin password. |
| `OPENAI_API_KEY` | *(empty)* | OpenAI API key for AI Ops incident diagnosis. |
| `AI_BASE_URL` | `https://api.openai.com/v1` | OpenAI API compatible base URL. |
| `AI_MODEL` | `gpt-4o-mini` | LLM model used for root-cause analysis. |

---

## 16. Developer Extension Cookbook (Step-by-Step Guides)

### 16.1 How to Add a New Microservice

Suppose you want to add a new service named `analytics` on port `8022`.

#### Step 1: Create Directory Structure
```powershell
mkdir services\analytics\cmd
mkdir services\analytics\internal\handlers
mkdir services\analytics\internal\store
mkdir services\analytics\migrations
```

#### Step 2: Initialize `go.mod`
Create `services/analytics/go.mod`:
```go
module github.com/jp-cloud/analytics

go 1.22

require (
    github.com/jp-cloud/go-common v0.0.0
    github.com/jp-cloud/packages/events v0.0.0
)

replace github.com/jp-cloud/go-common => ../../packages/go-common
replace github.com/jp-cloud/packages/events => ../../packages/events
```

#### Step 3: Register in Monorepo `go.work`
Edit `go.work` in monorepo root and append:
```
use ./services/analytics
```

#### Step 4: Write Migration File
Create `services/analytics/migrations/analytics_001_init.sql`:
```sql
CREATE TABLE IF NOT EXISTS project_pageviews (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id UUID NOT NULL,
    project_id UUID NOT NULL,
    path TEXT NOT NULL,
    referrer TEXT,
    ip TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_pageviews_project ON project_pageviews(project_id, created_at DESC);
```

#### Step 5: Implement `cmd/main.go`
```go
package main

import (
    "context"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/jp-cloud/analytics/internal/handlers"
    "github.com/jp-cloud/analytics/internal/store"
    "github.com/jp-cloud/go-common/config"
    jwtutil "github.com/jp-cloud/go-common/jwt"
    "github.com/jp-cloud/go-common/logging"
    "github.com/jp-cloud/go-common/middleware"
    "github.com/jp-cloud/go-common/postgres"
)

func main() {
    cfg, err := config.Load("8022")
    if err != nil { panic(err) }
    log := logging.New(cfg.LogLevel)
    ctx := context.Background()

    pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
    if err != nil { log.Error("db error", "err", err); os.Exit(1) }
    defer pool.Close()

    migDir := os.Getenv("MIGRATIONS_DIR")
    if migDir == "" { migDir = "migrations" }
    if err := postgres.Migrate(ctx, pool, migDir); err != nil {
        log.Error("migrate error", "err", err); os.Exit(1)
    }

    jm := jwtutil.NewManager(cfg.JWTSecret, cfg.JWTAccessTTL, cfg.JWTRefreshTTL)
    h := &handlers.Handler{
        Store: store.New(pool),
        JWT:   jm,
    }

    mux := http.NewServeMux()
    h.Routes(mux)
    handler := middleware.Chain(mux, middleware.RequestID, middleware.Logging(log), middleware.CORS(cfg.CORSOrigins))

    srv := &http.Server{Addr: ":" + cfg.HTTPPort, Handler: handler}
    go srv.ListenAndServe()

    stop := make(chan os.Signal, 1)
    signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
    <-stop
    srv.Shutdown(context.Background())
}
```

#### Step 6: Mount in Docker Compose & Gateway
1. In `infra/compose/docker-compose.yml`, add the `analytics` service block with build args and port mapping `8022:8022`.
2. In `services/gateway/cmd/main.go`, instantiate reverse proxy:
   ```go
   analyticsSvc, err := proxy.New(cfg.AnalyticsURL)
   mux.Handle("GET /api/v1/orgs/{orgId}/projects/{projectId}/analytics", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(analyticsSvc, "/api/v1")))))
   ```
3. Add `ANALYTICS_URL=http://analytics:8022` to `.env.example`.

---

### 16.2 How to Add a New API Route

Suppose you want to add an endpoint to purge a project's build cache: `POST /api/v1/orgs/{orgId}/projects/{projectId}/builds/cache/purge`.

1. **Implement Handler in `services/build/internal/handlers/handlers.go`:**
   ```go
   func (h *Handler) PurgeCache(w http.ResponseWriter, r *http.Request) {
       projectID := r.PathValue("projectId")
       err := h.Store.PurgeProjectCache(r.Context(), projectID)
       if err != nil {
           httpx.Error(w, http.StatusInternalServerError, "internal_error", "failed to purge cache")
           return
       }
       httpx.JSON(w, http.StatusOK, map[string]bool{"purged": true})
   }
   ```
2. **Register Route in Handler Router:**
   ```go
   mux.Handle("POST /orgs/{orgId}/projects/{projectId}/builds/cache/purge", auth(http.HandlerFunc(h.PurgeCache)))
   ```
3. **Register Gateway Forwarder in `services/gateway/cmd/main.go`:**
   ```go
   mux.Handle("POST /api/v1/orgs/{orgId}/projects/{projectId}/builds/cache/purge", auth(http.HandlerFunc(withOrgContext(jm, httpClient, cfg.OrganizationURL, stripAndProxy(buildSvc, "/api/v1")))))
   ```
4. **Update `packages/openapi/openapi.yaml`:**
   Add path `/orgs/{orgId}/projects/{projectId}/builds/cache/purge` with POST method.
5. **Add Client Method in Dashboard (`apps/dashboard/src/lib/api.ts`):**
   ```typescript
   purgeBuildCache(orgId: string, projectId: string) {
       return request<{ purged: boolean }>(`/orgs/${orgId}/projects/${projectId}/builds/cache/purge`, {
           method: "POST"
       });
   }
   ```
6. **Add CLI Subcommand in `apps/cli/cmd/builds.go`:**
   ```go
   var purgeCacheCmd = &cobra.Command{
       Use: "purge-cache",
       Short: "Purge build cache for a project",
       RunE: func(cmd *cobra.Command, args []string) error {
           client, cfg, _ := newClient()
           return client.PurgeBuildCache(cfg.CurrentOrgID, deployProjectID)
       },
   }
   ```

---

### 16.3 How to Add a New Database Table & SQL Migration

1. Locate the responsible microservice under `services/<service>/migrations/`.
2. Determine next numeric index (e.g. `services/project/migrations/project_003_tags.sql`).
3. Write clean, idempotent DDL:
   ```sql
   CREATE TABLE IF NOT EXISTS project_tags (
       id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
       project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
       tag TEXT NOT NULL,
       created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
       UNIQUE (project_id, tag)
   );
   CREATE INDEX IF NOT EXISTS idx_project_tags_lookup ON project_tags(project_id);
   ```
4. Restart the service container. `postgres.Migrate()` automatically detects the file, executes it in a transaction, and marks it applied in `schema_migrations`.

---

### 16.4 How to Add a New Event Topic & Consumer Group

1. **Declare Topic and Event Types in `packages/events/events.go`:**
   ```go
   const TopicSecurity = "jp.security"
   const TypeSecurityVulnerabilityDetected = "security.vulnerability_detected"
   const SecurityConsumerGroup = "jp-security"
   ```
2. **Publish Event:**
   ```go
   env := events.New(events.TopicSecurity, events.TypeSecurityVulnerabilityDetected, actorID, orgID, map[string]any{
       "package": "openssl",
       "severity": "CRITICAL",
   })
   _, err := events.PublishJSON(ctx, rdb, events.TopicSecurity, env)
   ```
3. **Consume Event:**
   In your consumer daemon:
   ```go
   _ = events.EnsureGroup(ctx, rdb, events.TopicSecurity, events.SecurityConsumerGroup)
   msgs, _ := events.ReadGroup(ctx, rdb, events.TopicSecurity, events.SecurityConsumerGroup, "worker-1", 10, 2*time.Second)
   for _, msg := range msgs {
       env, _ := events.ParseEnvelope(msg)
       // Handle vulnerability event...
       _ = events.Ack(ctx, rdb, events.TopicSecurity, events.SecurityConsumerGroup, msg.ID)
   }
   ```

---

### 16.5 How to Add a New Add-on Engine to Marketplace

Suppose you want to add **Elasticsearch**:
1. In `services/addon/internal/catalog/catalog.go`, register the engine:
   ```go
   {
       ID: "elasticsearch",
       Name: "Elasticsearch",
       Category: "search",
       Description: "Distributed search and analytics engine",
   }
   ```
2. In `services/addon/internal/provisioner/provisioner.go`, implement the provisioner method:
   - In `simulate` mode, return `http://elastic:dev_pass@es-addon:9200`.
   - In `shared` mode, connect to Elasticsearch REST API and generate an index and API key.
3. Inject the connection URL into `secret:8014` with key `ELASTICSEARCH_URL`.

---

### 16.6 How to Add a New CLI Command

Suppose you want to add `jp restart --project <id>`:
1. Create `apps/cli/cmd/restart.go`:
   ```go
   package cmd

   import (
       "fmt"
       "github.com/spf13/cobra"
   )

   var restartProjectID string

   var restartCmd = &cobra.Command{
       Use:   "restart",
       Short: "Restart active runtime instances for a project",
       RunE: func(cmd *cobra.Command, args []string) error {
           client, cfg, err := newClient()
           if err != nil { return err }
           if err := requireLogin(cfg); err != nil { return err }
           if restartProjectID == "" { return fmt.Errorf("--project is required") }

           err = client.RestartRuntime(cfg.CurrentOrgID, restartProjectID)
           if err != nil { return err }
           fmt.Println("Project instances restarted successfully.")
           return nil
       },
   }

   func init() {
       restartCmd.Flags().StringVar(&restartProjectID, "project", "", "Project ID")
       rootCmd.AddCommand(restartCmd)
   }
   ```

---

### 16.7 How to Add a New Dashboard Page & Component

Suppose you want to add a dedicated **Audit Log** page `/audit`:
1. Create page directory: `apps/dashboard/src/app/(console)/audit/page.tsx`.
2. Implement Page Component:
   ```tsx
   'use client';
   import { useEffect, useState } from 'react';
   import { useOrg } from '@/lib/org-context';
   import { api } from '@/lib/api';

   export default function AuditPage() {
       const { currentOrg } = useOrg();
       const [logs, setLogs] = useState([]);

       useEffect(() => {
           if (currentOrg) {
               // Call API and set state...
           }
       }, [currentOrg]);

       return (
           <div className="p-6">
               <h1 className="text-2xl font-bold">Security Audit Trail</h1>
               {/* Table rendering logs */}
           </div>
       );
   }
   ```
3. Add Navigation Link in `apps/dashboard/src/components/app-shell.tsx`:
   Add entry `{ href: '/audit', label: 'Audit Trail', icon: ShieldCheckIcon }` to the navigation array.

---

### 16.8 How to Connect Real Stripe Billing

The current `billing:8020` service tracks compute units via Redis Streams. To connect Stripe:
1. Add `github.com/stripe/stripe-go/v78` to `services/billing/go.mod`.
2. Add `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` to `.env.example`.
3. In `services/billing/internal/handlers/checkout.go`, implement:
   - `POST /billing/checkout`: Creates a Stripe Checkout Session for subscription tier `pro` or `scale`.
   - `POST /billing/webhook`: Ingests `customer.subscription.created`, `invoice.payment_succeeded`, and `customer.subscription.deleted`.
4. In `services/billing/internal/store/plans.go`, update `billing_org_plans` on successful payment webhook.

---

## 17. Operational Runbook, Troubleshooting & Diagnostics

### 17.1 Local Quickstart (Windows & Linux/macOS)

#### Windows PowerShell:
```powershell
# 1. Clone & prepare environment
cd "c:\Users\Zeeshan\Music\cloud-plateform"
Copy-Item .env.example .env

# 2. Detect host size & generate resource overlays
.\scripts\size-host.ps1

# 3. Start core platform + UI
.\scripts\jp-up.ps1 -Profiles platform,ui

# 4. Compile CLI
cd apps\cli
go build -o jp.exe .
.\jp.exe whoami
```

#### Linux / macOS Bash:
```bash
# 1. Clone & prepare environment
cd /path/to/cloud-plateform
cp .env.example .env

# 2. Make scripts executable & detect sizing
chmod +x ./scripts/*.sh
./scripts/size-host.sh

# 3. Start core platform + UI
./scripts/jp-up.sh platform,ui

# 4. Compile CLI
cd apps/cli
go build -o jp .
./jp whoami
```

### 17.2 Diagnostic Commands & Health Checks

#### Verify Gateway Health:
```powershell
curl http://localhost:8000/healthz
# Expected: {"status":"ok"}
```

#### Inspect Redis Stream Message Lag:
```powershell
docker exec -it jp-redis-1 redis-cli XINFO GROUPS jp.build
docker exec -it jp-redis-1 redis-cli XINFO GROUPS jp.deploy
```

#### Inspect Traefik Active Routers:
Open `http://localhost:8080/dashboard/` in your browser. Verify HTTP routers for `gateway-router` and per-project domain host routers.

#### Inspect Database Tables:
```powershell
docker exec -it jp-postgres-1 psql -U jp -d jp -c "\dt"
docker exec -it jp-postgres-1 psql -U jp -d jp -c "SELECT * FROM schema_migrations;"
```

### 17.3 Common Failure Modes & Recovery Procedures

#### 1. Worker Cannot Push to Image Registry
- **Symptom:** Build fails with `Get "http://host.docker.internal:5000/v2/": dial tcp: lookup host.docker.internal: no such host`.
- **Cause:** Docker daemon cannot resolve `host.docker.internal` or port 5000 is blocked.
- **Fix:** In Docker Desktop Settings → Docker Engine, ensure `insecure-registries` includes `host.docker.internal:5000`:
  ```json
  {
    "insecure-registries": ["host.docker.internal:5000"]
  }
  ```

#### 2. Domain Verification Fails
- **Symptom:** `POST .../domains/{id}/verify` returns `{"verified": false}`.
- **Cause:** DNS propagation delay or local hostname cannot resolve.
- **Fix:** Use `--force` flag on CLI or set `DOMAIN_DNS_STUB=true` in `.env`:
  ```powershell
  .\jp.exe domains verify <domain-id> --project <project-id> --force
  ```

#### 3. Database Migration Deadlock
- **Symptom:** Microservice hangs on boot with `migrate error`.
- **Cause:** A previous migration crashed mid-execution without rolling back its lock.
- **Fix:** Inspect PostgreSQL active locks and kill stuck queries:
  ```sql
  SELECT pid, query, state FROM pg_stat_activity WHERE state != 'idle';
  SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE query LIKE '%schema_migrations%';
  ```

#### 4. Redis Stream Worker Dead Letter / Pending Lag
- **Symptom:** Builds remain in `queued` indefinitely; worker does not pick up new jobs.
- **Cause:** Unacknowledged messages accumulated in consumer group PEL.
- **Fix:** Inspect and claim pending messages:
  ```powershell
  docker exec -it jp-redis-1 redis-cli XPENDING jp.build jp-workers
  ```
