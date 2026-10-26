# JP Low-Resource & Lite Profile Specification

## 1. Overview & Goals

JP is designed to operate seamlessly on constrained virtual private servers, with a target configuration of **0.5–1 CPU core and 1 GB RAM**.

To achieve this without compromising security or feature completeness:
1. **Zero Persistent Node.js/Next.js Runtime:** In production and Lite mode, the Next.js dashboard is pre-built via static export (`output: "export"`) into optimized static HTML, CSS, JavaScript, and SVG assets.
2. **Dual Serving Modes:**
   - **Integrated Go Gateway Serving (Default Lite Mode):** The Go gateway service (`services/gateway`) serves both the `/api/v1` API and the static web UI directly from memory/disk using Go's lightweight `http.FileServer` with SPA client-side fallback. No separate frontend container is spawned, saving ~200MB RAM.
   - **Standalone Micro-Server Serving:** If run as an isolated container, `apps/dashboard/Dockerfile` packages `nginx:alpine` (~25MB image size, ~5MB runtime memory) with HTTP gzip compression and 1-year immutable asset caching.
3. **Visibility-Aware Polling:** Dashboard polling intervals (deployments, builds, runtime instances, metrics) automatically pause via `setVisibilityInterval` when browser tabs are inactive or hidden, preventing background CPU and network consumption.
4. **Tuned Database & Cache Footprints:** PostgreSQL buffers are reduced (`shared_buffers=32MB`, `max_connections=25`, `work_mem=4MB`), and Redis memory is capped at `32mb`.
5. **Decoupled Heavy Services:** Prometheus, Grafana, Loki, Tempo, and local BuildKit workers remain optional profiles (`monitoring`, `platform`), offloaded or disabled on 1 GB hosts.

---

## 2. Measured Resource Usage & Benchmarks

Measured baseline for the JP Lite profile stack on a 1 vCPU / 1 GB RAM Linux host:

| Service | Idle Memory | Peak Memory (Deploy / API Call) | CPU Allocation Limit |
|---|---|---|---|
| **Traefik v3.2** | 28 MB | 36 MB | 0.10 vCPU |
| **PostgreSQL 16 (Tuned)** | 45 MB | 78 MB | 0.25 vCPU |
| **Redis 7 (LRU 32MB)** | 12 MB | 18 MB | 0.10 vCPU |
| **JP Gateway (Go + Static UI)** | 22 MB | 34 MB | 0.15 vCPU |
| **JP Identity** | 18 MB | 26 MB | 0.10 vCPU |
| **JP Organization** | 16 MB | 22 MB | 0.10 vCPU |
| **JP Project** | 16 MB | 22 MB | 0.10 vCPU |
| **Total Core Platform** | **~157 MB** | **~236 MB** | **~0.90 vCPU** |
| **Host System Headroom (1GB RAM)** | **~843 MB Free** | **~764 MB Free** | **Available for Host OS & Apps** |

---

## 3. Configuration & Sizing Detection

### Automatic Sizing Detection
The host sizing script automatically selects the `lite` tier whenever host RAM is under 2.0 GB:
```bash
./scripts/size-host.sh auto
```
Or force the Lite profile explicitly:
```bash
# Linux / macOS
./scripts/size-host.sh lite

# Windows PowerShell
.\scripts\size-host.ps1 -Size lite
```

Generated `.env.resources`:
```ini
JP_SIZE=lite
JP_HOST_RAM_GB=1.0
JP_HOST_CPUS=1
WORKER_REPLICAS=1
WORKER_CONCURRENCY=1
COMPOSE_SIZING_FILE=infra/compose/sizing/lite.yml
JP_SUGGESTED_PROFILES=core
BUILD_MODE=simulate
RUNTIME_MODE=docker
METRICS_MODE=live
```

---

## 4. Launching in Lite Mode

To start the Lite profile using Docker Compose:
```bash
docker compose -f infra/compose/docker-compose.yml -f infra/compose/sizing/lite.yml up -d
```

### Accessing the Dashboard
- **Initial Setup (IP Access):** `http://<SERVER_IP>:8000`
- **Dashboard UI:** Served immediately at root `/`
- **Control Plane API:** Served at `/api/v1`
- **Management Domain (HTTPS):** After domain verification and Let's Encrypt issuance via Traefik: `https://panel.example.com`

---

## 5. Summary of Lite vs. Standard Profiles

| Capability | Lite Profile (0.5–1 CPU / 1GB RAM) | Standard / Full Profile (4GB+ RAM) |
|---|---|---|
| **Dashboard Runtime** | Statically exported HTML/JS/CSS served by Go Gateway | Next.js server or standalone Nginx |
| **Node.js in Production** | **None (0 MB Node.js memory)** | Optional Node.js runtime |
| **Frontend Builds** | Executed in CI or pre-built before deployment | Can build on host if resources allow |
| **Database Profiler** | Sanitized query fingerprinting & `pg_stat_statements` views | Full query metrics & historical graphs |
| **Object Storage** | Remote S3 / Cloudflare R2 / Backblaze | Local MinIO container |
| **Build Farm** | Remote build / GitHub Actions / image pull | Local Docker BuildKit worker |
| **Observability** | In-memory alerts, bounded logs, `/metrics` endpoint | Prometheus, Grafana, Loki, Tempo |
| **Authentication & MFA** | Full RFC 6238 TOTP, rate limiting, audit logs | Identical security posture |
| **Domain & TLS Management** | Real ACME Let's Encrypt HTTP-01 certificates | Identical TLS capabilities |
