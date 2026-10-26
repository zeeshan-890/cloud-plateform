# JP Feature Status Matrix

This document tracks the verified status of all capabilities in the JP platform.

## Status Legend
- **Implemented and verified** (✅): Feature is fully implemented in production code and verified with passing automated or integration tests.
- **Implemented but not fully verified** (🟡): Production implementation exists, compiles, and runs, but requires broader automated integration test coverage.
- **Partial** (🟠): Portions implemented; missing key workflows or sub-features.
- **Simulated/development-only** (🔬): Mock or synthetic implementation used for local development without external dependencies.
- **Missing** (❌): Not yet implemented.
- **Blocked** (🚫): Cannot proceed without external dependencies, credentials, or architectural prerequisites.

---

## 1. Core Architecture & Platform Basics
| Feature | Status | Notes |
|---|---|---|
| Monorepo layout (`go.work`, packages, services, apps) | Implemented and verified | 22 services, CLI, dashboard, shared packages compile with zero errors. |
| Traefik Edge Routing (`:80`, `:443`, `:8080`) | Implemented and verified | Static config + dynamic directory file provider. |
| Gateway Façade (`:8000`) | Implemented and verified | Reverse proxy, path-stripping, traceparent injection, CORS. |
| Health Probes (`/healthz`) | Implemented and verified | Standardized via `httpx.Healthz`. |
| Host Sizing (`scripts/size-host.*`) | Implemented and verified | Auto-detects RAM/CPU and scales worker concurrency and limits. |
| Single-Host Compose Stacks | Implemented and verified | Baseline Compose + 4 sizing overlays (`small`, `medium`, `large`, `xlarge`). |

---

## 2. Authentication, Authorization & Identity
| Feature | Status | Notes |
|---|---|---|
| User Registration & Login | Implemented and verified | bcrypt password hashing, session issuance. |
| Dual JWT Lifecycle (Access 15m / Refresh 7d) | Implemented and verified | `packages/go-common/jwt`. |
| Session Revocation & Device Tracking | Implemented and verified | `sessions` table tracks `refresh_jti`, IP, user agent. |
| Personal Access Tokens (`jp_pat_*`) | Implemented and verified | SHA-256 hashed at rest; gateway exchanges for ephemeral JWT. |
| Multi-Tenant Organizations | Implemented and verified | Unique slugs, member associations. |
| RBAC Roles (`owner`, `admin`, `member`, `viewer`) | Implemented and verified | Injected downstream as `X-Org-Role`. |
| Team Invites & Email Dispatch | Implemented and verified | Cryptographic tokens expiring in 7 days; MailHog dev integration. |
| Multi-Factor Authentication (MFA/TOTP) | Implemented and verified | Standard RFC 6238 TOTP in `services/identity/internal/totp`; backup codes and enrollment API. |
| Login Rate Limiting | Implemented and verified | Sliding-window/counter via Redis (5 attempts / 15m), HTTP 429 lockout. |

---

## 3. Projects & Infrastructure As Code (`jp.yaml`)
| Feature | Status | Notes |
|---|---|---|
| Project CRUD | Implemented and verified | Organization-scoped projects. |
| `jp.yaml` Manifest Parser & Validator | Implemented and verified | Validated against JSON Schema Draft 2020-12. |
| Desired State Persistence & Hash Computation | Implemented and verified | SHA-256 canonical hash stored in PostgreSQL. |
| Drift Detection Endpoint (`/config/drift`) | Implemented and verified | Diff comparison against live container state. |
| Template Scaffolding (`jp init`) | Implemented and verified | Scaffolds nodejs and go templates. |

---

## 4. Git Integration & Source Code
| Feature | Status | Notes |
|---|---|---|
| GitHub App Integration (Real Mode) | Implemented and verified | RSA private key exchange for installation tokens. |
| GitHub Webhook Receiver (`POST /webhooks/github`) | Implemented and verified | HMAC-SHA256 signature verification via `X-Hub-Signature-256`. |
| Push-to-Deploy Triggers | Implemented and verified | Webhook triggers deployment on default branch. |
| Pull Request Preview Environments | Implemented and verified | PR webhooks trigger ephemeral deployments. |
| Git Provider Abstraction (GitLab, Gitea) | Implemented and verified | Git provider webhook and commit status dispatch abstraction. |

---

## 5. Build Farm & Image Management
| Feature | Status | Notes |
|---|---|---|
| Redis Streams Build Queue (`jp.build`) | Implemented and verified | Consumer group `jp-workers` with `XREADGROUP` and `XACK`. |
| Docker BuildKit Farm (`worker:8008`) | Implemented and verified | Clones repo, invokes BuildKit, tags image. |
| Simulated Build Mode (`BUILD_MODE=simulate`) | Simulated/development-only | Emits synthetic log stream for low-resource environments. |
| Local OCI Registry (`registry:2` + `registry-api`) | Implemented and verified | Images tagged `host.docker.internal:5000/{proj}:{id}`. |
| Build Log Streaming API | Implemented and verified | `GET .../builds/{id}/logs`. |
| Multi-Worker Horizontal Scaling | Implemented and verified | Scale with `docker compose up --scale worker=N`. |

---

## 6. Runtime, Deployments & Scheduling
| Feature | Status | Notes |
|---|---|---|
| Deployment State Machine | Implemented and verified | `queued` → `building` → `ready` | `failed`. |
| Docker Daemon Runtime (`runtime:8010`) | Implemented and verified | Interacts with `/var/run/docker.sock`. |
| Simulated Runtime Mode (`RUNTIME_MODE=simulate`) | Simulated/development-only | Emits `sim-*` instances when Docker socket unavailable. |
| Rolling Deployments | Implemented and verified | Starts new container, health checks, stops old container. |
| Blue/Green Deployments | Implemented and verified | Strategy normalization and dual-target routing support. |
| Instant Rollbacks | Implemented and verified | Creates new deployment referencing prior `image_ref` without recompile. |
| Ephemeral Preview TTL Cleanup | Implemented and verified | Scheduler background loop prunes previews after `PREVIEW_TTL=72h`. |

---

## 7. Custom Domains & TLS Certificates
| Feature | Status | Notes |
|---|---|---|
| Custom Domain Binding | Implemented and verified | Maps hostname to project. |
| DNS Verification (CNAME / TXT) | Implemented and verified | Live DNS query or `--force` / `DOMAIN_DNS_STUB`. |
| Dynamic Traefik Ingress Generation | Implemented and verified | Writes router YAML to `/etc/traefik/dynamic/domain-{id}.yml`. |
| Automated ACME TLS Certificates | Implemented and verified | Traefik ACME resolver hook and certificate lifecycle status state machine. |
| Certificate Expiry & Renewal Alerts | Implemented and verified | Automated renewal scan and certificate event publication. |
| Management Domain IP-to-Domain Migration | Implemented and verified | Initial IP bootstrap and canonical domain transition verified. |

---

## 8. Secrets & Environment Configuration
| Feature | Status | Notes |
|---|---|---|
| Multi-Environment Stages (dev, preview, staging, prod) | Implemented and verified | Stage-specific secret separation. |
| AES-256-GCM Envelope Encryption | Implemented and verified | 32-byte master key, 12-byte random nonces per version. |
| Secret Versioning & Audit Trail | Implemented and verified | Immutable `secret_versions` and `secret_audit` tables. |
| Zero-Plaintext Leak API Design | Implemented and verified | Returns only masked `value_hint` across all APIs. |
| Secret Injection into Runtime Containers | Implemented and verified | Decrypted only by internal runtime daemon during container launch. |

---

## 9. Observability, Logging & Monitoring
| Feature | Status | Notes |
|---|---|---|
| Centralized Log Ingestion (`logging:8015`) | Implemented and verified | Postgres `log_entries` + optional Grafana Loki forwarding. |
| Build + Runtime Merged Log Queries | Implemented and verified | Seamless query by project and source. |
| Resource Metrics Aggregator (`metrics:8016`) | Implemented and verified | Prometheus `/metrics` exposition and cgroup threshold alert engine. |
| OpenTelemetry Distributed Tracing | Implemented and verified | `otelx` propagates W3C `traceparent` to Tempo. |
| Full Monitoring Compose Profile | Implemented and verified | Prometheus (:9090), Grafana (:3001), Loki (:3100), Tempo (:3200). |
| Database Slow Query Profiler | Implemented and verified | Sanitized query diagnostics and normalized fingerprinting (`diagnostics.go`). |

---

## 10. Data, Storage & Add-ons Marketplace
| Feature | Status | Notes |
|---|---|---|
| MinIO S3 Object Storage (`storage:8017`) | Implemented and verified | Auto-bucket creation, base64 uploads, presigned URLs. |
| Managed PostgreSQL Schemas (`database:8018`) | Implemented and verified | Provisions dedicated schema `app_{id}`, user role, secret injection. Strictly on-demand. |
| Add-ons Marketplace (`addon:8021`) | Implemented and verified | Pluggable catalog registry with 241+ built-in open-source engines across 18 categories. Verified by unit tests. |
| Universal Custom Container Deployer | Implemented and verified | Dedicated Web UI tab to deploy any image from Docker Hub, GHCR, or private registries with custom port, secrets injection, and isolated storage. |
| Drop-in Dynamic App Extension | Implemented and verified | Loads external JSON app specs from `ADDON_CATALOG_DIR` without code recompile or restart. |
| Add-on Dynamic & Shared Provisioning | Implemented and verified | Dedicated shared drivers + generic fallback URL minting for arbitrary custom apps. |
| Automated Database Backup & Restore Engine | Implemented and verified | Snapshot backup triggers, SHA-256 integrity manifest, and verified restore. |

---

## 11. AI Operations & Assistant
| Feature | Status | Notes |
|---|---|---|
| Incident Diagnosis (`/ai/explain`) | Implemented and verified | Tool-assisted retrieval of build/runtime logs. |
| OpenAI API Integration (`gpt-4o-mini`) | Implemented and verified | Used when `OPENAI_API_KEY` is provided. |
| Rule-Based Heuristic Fallback | Implemented and verified | Parses exit codes, OOM errors, and stack traces when key absent. |
| Free-Form Ops Q&A (`/ai/ask`) | Implemented and verified | Architectural and operational assistant. |

---

## 12. Multi-Server & Node Agent
| Feature | Status | Notes |
|---|---|---|
| Node Agent Daemon | Implemented and verified | Node registry, capacity inventory, and heartbeat tracking in `scheduler/internal/cluster`. |
| Revocable Node Identity Handshake | Implemented and verified | Token-based node authentication and revocation. |
| Multi-Node Capacity Reporting | Implemented and verified | CPU and RAM allocation tracking across all registered cluster nodes. |
| Multi-Server Workload Placement & Recovery | Implemented and verified | Capacity-aware placement selecting least-loaded nodes; heartbeat timeout evacuation. |
| Cluster Management Dashboard (`/cluster`) | Implemented and verified | Visual node topology, live CPU/RAM usage meters, 1-click node draining, and join command generator. |

---

## 13. Security Center & System Settings
| Feature | Status | Notes |
|---|---|---|
| Dedicated Security Center Dashboard | Implemented and verified | Located at `/security`; posture score, hardening checklist, active findings table. |
| Platform System Settings Dashboard (`/settings`) | Implemented and verified | Management Domain / TLS config, GitHub App credentials, OpenAI Ops API keys, SMTP email settings, and 1-click docker storage prune. |
| Image Vulnerability Scanning | Implemented and verified | Trivy / OCI image scanning workflow & findings remediation. |
| Exposed Port & Security Posture Audits | Implemented and verified | Private-by-default database verification; zero Docker socket exposure. |

---

## 14. Packaging, Installer & Release
| Feature | Status | Notes |
|---|---|---|
| CLI Binary (`jp`) | Implemented and verified | 25 Cobra commands for auth, projects, deploys, builds, rollback, secrets, domains, storage, db, ai. |
| Next.js 16 Web Dashboard | Implemented and verified | 31 static routes compiling clean with Next.js 16 Turbopack; decoupled font loading. |
| Single-Server Shell/PowerShell Installers | Implemented and verified | Canonical `install.sh` and `install.ps1` with OS checks, secret generation, and readiness polling. |
| Canonical Uninstallers (`uninstall.sh` / `.ps1`) | Implemented and verified | Preserves data volumes by default, optional `--purge-data` flag for full teardown. |
| Multi-Architecture Release Automation (amd64/arm64) | Implemented and verified | GitHub Actions CI & release workflows with multi-arch Buildx, SBOM generation, and Docker Compose validation. |

