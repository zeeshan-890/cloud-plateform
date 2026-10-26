# JP Installation & Deployment Guide

> **Single-Server Installation, Hardware Sizing Profiles, and Setup Runbook.**

---

## 1. Prerequisites

- **Supported Operating Systems:** Ubuntu 22.04+ LTS, Debian 12+, RHEL 9+, macOS, Windows 10/11 (with WSL2).
- **Runtime:** Docker Engine 24.0+ with Docker Compose v2.
- **Network Ports:** Port 80 (HTTP), Port 443 (HTTPS), Port 8000 (Gateway API), Port 8080 (Traefik Dashboard).

---

## 2. Resource Profiles

| Profile | Hardware Minimum | Strategy & Services |
|---|---|---|
| **Lite** | ~1 GB RAM, 1 vCPU | Gateway, Identity, Org, Project, Deploy, Traefik, PostgreSQL, Redis. Remote builds and external storage recommended. Monitoring disabled. |
| **Standard** | 6–16 GB RAM, 4 vCPUs | Adds Build Worker, OCI Registry, MinIO S3, Managed Database engine, and Web Dashboard. |
| **Cluster** | Multiple Servers | Adds Node Agent, Distributed Build Workers, and Observability Stack. |

---

## 3. Quickstart Installation

### Step 1: Clone Repository & Configure Environment
```bash
git clone https://github.com/jp-cloud/cloud-plateform.git /opt/jp
cd /opt/jp
cp .env.example .env
```

### Step 2: Adaptive Hardware Sizing
Run the hardware detection script to auto-generate `.env.resources`:
```bash
# Linux / macOS
chmod +x ./scripts/*.sh
./scripts/size-host.sh

# Windows PowerShell
.\scripts\size-host.ps1
```

### Step 3: Launch Platform
Start the core services and UI:
```bash
# Standard Launch
./scripts/jp-up.sh platform,ui

# Windows PowerShell
.\scripts\jp-up.ps1 -Profiles platform,ui
```

### Step 4: Access Console
- **Web Dashboard:** `http://SERVER_IP:3000`
- **Gateway Health:** `http://SERVER_IP:8000/healthz`
- **Traefik Proxy Console:** `http://SERVER_IP:8080`

---

## 4. Initial Bootstrap Flow

1. On first launch, open `http://SERVER_IP:3000/register`.
2. Register the initial administrative account.
3. Create your primary organization.
4. Navigate to **System Settings → Domain** to migrate from initial IP access to a canonical management domain (e.g. `panel.example.com`).

---

## 5. Canonical Installer

For automated single-command installation:

```bash
# Linux / macOS (auto-detects hardware and starts healthy platform)
curl -fsSL https://get.jpcloud.dev | bash
# or locally:
./install.sh [lite|small|medium|large|xlarge|auto]

# Windows PowerShell:
.\install.ps1 -Size auto
```

---

## 6. Uninstallation & Data Preservation

To stop and remove platform containers while preserving persistent volumes and database data:

```bash
# Linux / macOS
./uninstall.sh

# Windows PowerShell
.\uninstall.ps1
```

To completely purge all platform data, databases, and secrets:

```bash
# Linux / macOS
./uninstall.sh --purge-data

# Windows PowerShell
.\uninstall.ps1 -PurgeData
```

