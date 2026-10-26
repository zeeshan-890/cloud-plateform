# JP Cloud Platform — Production Installation & Server Setup Guide

> **Official Step-by-Step Runbook for Deploying JP on a Fresh Linux Server, Cloud VPS, or Multi-Node Cluster.**

---

## Table of Contents
1. [Server Requirements & Hardware Sizing](#1-server-requirements--hardware-sizing)
2. [Server Preparation & Firewall Configuration](#2-server-preparation--firewall-configuration)
3. [Installing Docker Engine & Docker Compose V2](#3-installing-docker-engine--docker-compose-v2)
4. [Installing JP via the Canonical Automated Installer](#4-installing-jp-via-the-canonical-automated-installer)
5. [First-Time Bootstrap & Admin Account Setup](#5-first-time-bootstrap--admin-account-setup)
6. [Configuring a Custom Domain & Free Let's Encrypt SSL](#6-configuring-a-custom-domain--free-lets-encrypt-ssl)
7. [Configuring Platform Settings (GitHub App, AI Ops, SMTP)](#7-configuring-platform-settings-github-app-ai-ops-smtp)
8. [Deploying Your First Application or Marketplace Service](#8-deploying-your-first-application-or-marketplace-service)
9. [Scaling from One Server to Multiple Servers (Cluster)](#9-scaling-from-one-server-to-multiple-servers-cluster)
10. [Maintenance, Updates & Disaster Recovery](#10-maintenance-updates--disaster-recovery)
11. [Troubleshooting & FAQs](#11-troubleshooting--faqs)

---

## 1. Server Requirements & Hardware Sizing

JP adapts its resource allocations automatically based on your server's available RAM and CPU cores.

### Hardware Sizing Profiles

| Profile | Target Specifications | Idle RAM Footprint | What It Runs | Best For |
|---|---|---|---|---|
| **Lite** | 1 vCPU, 1–2 GB RAM | **~250 MB** | Core Gateway, Identity, Org, Project, Deploy, Traefik, SQLite/PostgreSQL, Redis. | Cheap VPS ($3–$5/mo on Hetzner, DigitalOcean, Linode), staging servers. |
| **Small** | 2 vCPUs, 4 GB RAM | **~450 MB** | Core services + Build Worker + MinIO S3 Object Storage + Web Dashboard. | Standard developer servers, single-team installations. |
| **Medium** | 4 vCPUs, 8–16 GB RAM | **~750 MB** | Full stack + concurrent build workers + monitoring (Prometheus, Grafana, Loki). | Production workloads with continuous Git deployments. |
| **Large / Cluster** | 8+ vCPUs, 32+ GB RAM | Adaptive | Distributed cluster control plane with multi-node scheduling and capacity balancing. | High-traffic multi-server production infrastructure. |

### Supported Operating Systems
- **Ubuntu:** 24.04 LTS, 22.04 LTS, 20.04 LTS *(Recommended)*
- **Debian:** 12 (Bookworm), 11 (Bullseye)
- **RHEL / Rocky / AlmaLinux:** 9+
- **macOS / Windows:** Supported for local development via Docker Desktop or WSL2.

---

## 2. Server Preparation & Firewall Configuration

Connect to your server via SSH as `root` or a user with `sudo` privileges:

```bash
ssh root@YOUR_SERVER_IP
```

### 1. Update Server Packages
```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y git curl wget ufw ca-certificates gnupg
```

### 2. Configure Firewall (UFW)
Open the required public and internal ports:

```bash
# Allow SSH access (CRITICAL: do not skip or you may lock yourself out)
sudo ufw allow 22/tcp

# Public Web Traffic & Automated Let's Encrypt SSL Challenge
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp

# Initial Web Dashboard Port (before setting up your custom domain)
sudo ufw allow 3000/tcp

# API Gateway & Traefik Dashboard (optional: can be restricted to local/SSH tunnel)
sudo ufw allow 8000/tcp
sudo ufw allow 8080/tcp

# Enable Firewall
sudo ufw --force enable
sudo ufw status
```

---

## 3. Installing Docker Engine & Docker Compose V2

JP runs on standard Docker Engine 24.0+ and Docker Compose v2.

Run the official Docker automated installation script:

```bash
# 1. Download and run official Docker install script
curl -fsSL https://get.docker.com | sh

# 2. Add your current user to the docker group
sudo usermod -aG docker $USER

# 3. Enable Docker service on boot
sudo systemctl enable --now docker

# 4. Verify installation
docker --version
docker compose version
```

---

## 4. Installing JP via the Canonical Automated Installer

JP includes a self-sizing canonical installer script that provisions secrets, detects hardware capacity, and starts all services.

### Step 1: Clone the Repository
```bash
sudo git clone https://github.com/zeeshan-890/cloud-plateform.git /opt/jp
sudo chown -R $USER:$USER /opt/jp
cd /opt/jp
```

### Step 2: Run the Installer
```bash
# Make installation scripts executable
chmod +x ./install.sh ./scripts/*.sh

# Run the installer with automatic hardware detection
./install.sh auto
```

> [!TIP]
> **Manual Profile Override:**
> If you are running on a 1GB RAM VPS and want to force the ultra-lightweight profile, run:
> ```bash
> ./install.sh lite
> ```

### What the Installer Automates Behind the Scenes:
1. **OS & Docker Verification:** Checks that Docker 24+ and Compose V2 are available.
2. **Dynamic Hardware Sizing:** Calculates RAM and CPU limits, writes `.env.resources`, and applies the optimal Compose overlay (`infra/compose/sizing/{lite,small,medium,large}.yml`).
3. **Cryptographic Secrets Generation:** Mints 32-byte secure random keys for:
   - `JWT_SECRET` (User session & token signing).
   - `SECRETS_MASTER_KEY` (AES-256-GCM envelope encryption for project environment variables).
   - `POSTGRES_PASSWORD` and database credentials.
4. **Booting Containers:** Spawns Traefik edge proxy, API Gateway, Identity, Projects, Scheduler, and Web Dashboard.
5. **Healthcheck Polling:** Verifies that all services report healthy before completing.

---

## 5. First-Time Bootstrap & Admin Account Setup

Once the installer finishes, it displays the access URLs:

```text
==========================================================
    JP Platform is live and ready!
==========================================================
Web Console:    http://YOUR_SERVER_IP:3000
API Gateway:    http://YOUR_SERVER_IP:8000/healthz
Traefik Edge:   http://YOUR_SERVER_IP:8080
```

1. Open your browser and navigate to:
   ```text
   http://YOUR_SERVER_IP:3000/register
   ```
2. **Register the Admin Account:** Enter your admin name, email, and a strong password. The first registered account automatically receives the platform `owner` role.
3. **Create Your Organization:** Enter your organization name (e.g. `Acme Corp`) and project name.
4. You are now inside the **JP Web Console**!

---

## 6. Configuring a Custom Domain & Free Let's Encrypt SSL

To move away from raw IP addresses and access your platform securely over `https://`:

### Step 1: Create an A Record in Your DNS Provider
In your DNS registrar (Cloudflare, Namecheap, Route53, GoDaddy, etc.):
- **Type:** `A`
- **Name:** `panel` *(or `@` for root domain)*
- **Value:** `YOUR_SERVER_IP`
- **Proxy Status:** DNS Only *(grey cloud if using Cloudflare during initial cert issuance)*
- **TTL:** Auto (or 300s)

### Step 2: Activate Domain in the Web Dashboard
1. In the JP Dashboard, click **System Settings (`/settings`)** in the left sidebar.
2. Under **Management Domain & TLS**:
   - **Management Domain:** `panel.yourdomain.com`
   - **Let's Encrypt Admin Email:** `admin@yourdomain.com`
   - Click **Save & Apply Domain**.
3. Traefik automatically contacts Let's Encrypt, completes the HTTP-01 challenge, issues an SSL certificate, and redirects traffic to HTTPS.
4. You can now access your dashboard securely at **`https://panel.yourdomain.com`**.

---

## 7. Configuring Platform Settings (GitHub App, AI Ops, SMTP)

All platform integrations are managed visually from **`/settings`**:

### GitHub App (Push-to-Deploy & PR Previews)
1. Go to **Settings → GitHub App Integration**.
2. Click **Create GitHub App Configuration**.
3. Supply your **App ID**, **Client ID**, **Client Secret**, and paste your **RSA Private Key (`.pem`)**.
4. Now, any code push to your repositories will automatically trigger builds, preview environments, and rollbacks.

### AI Operations Assistant
1. In **Settings → AI Assistant & Operations Engine**:
2. Paste your `OPENAI_API_KEY` (or custom endpoint URL).
3. The platform's built-in AI will automatically diagnose build failures, analyze OOM crashes, explain runtime logs, and suggest code fixes.
*(If no key is configured, JP seamlessly falls back to its deterministic rule-based heuristic engine).*

### SMTP Email Alerts
1. In **Settings → SMTP Email Delivery**:
2. Supply your SMTP host (SendGrid, Mailgun, Postmark, AWS SES, or Gmail), port (587), and credentials.
3. Used for team member invites, password resets, and critical certificate expiration alerts.

---

## 8. Deploying Your First Application or Marketplace Service

JP features strict **on-demand provisioning** — no databases or background services run until you explicitly request them.

### Deploying from the 240+ One-Click Marketplace
1. Go to **Add-ons & Marketplace (`/addons`)**.
2. Filter through the 18 categories (`Databases`, `AI & LLMs`, `Media & Streaming`, `Networking & VPN`, `Dev Tools`, etc.) or search for your favorite application (e.g. `PostgreSQL`, `Redis`, `Jellyfin`, `n8n`, `Ollama`).
3. Click **Provision**, pick an instance name (e.g. `primary-db`), and click **Deploy**.
4. The service will be spun up in an isolated container with persistent storage, and connection credentials (`POSTGRES_URL_PRIMARY_DB`) will be injected directly into your project's environment variables.

### Deploying Any Custom Docker Container
If your application is not in the marketplace:
1. In **`/addons`**, click the **`+ Deploy Any Custom Container`** tab.
2. Supply:
   - **Instance Name:** `my-custom-app`
   - **Engine ID:** `custom-api`
   - **Docker Image Reference:** Any public or private image (e.g. `caddy:alpine`, `ghcr.io/org/repo:v1.0.0`).
   - **Exposed Port:** Main container port (e.g. `8080`).
3. Click **Deploy Custom Container**. JP handles volume mounts, network bridges, and health monitoring automatically.

---

## 9. Scaling from One Server to Multiple Servers (Cluster)

JP allows expanding from a single VPS to a multi-node distributed cluster:

1. In the Web Console, navigate to **Cluster & Servers (`/cluster`)**.
2. Click **Generate Join Command**.
3. The dashboard will generate a cryptographically signed join token and registration command:
   ```bash
   curl -fsSL https://panel.yourdomain.com/api/cluster/join | bash -s -- --token <NODE_TOKEN> --master https://panel.yourdomain.com
   ```
4. SSH into your **new second server** and run the join command.
5. The secondary server will register its CPU and RAM capacity with the JP scheduler.
6. New container workloads and preview environments will automatically be distributed to the least-loaded server!

---

## 10. Maintenance, Updates & Disaster Recovery

### Checking Platform Status & Logs
```bash
cd /opt/jp

# View status of all platform services
docker compose -f infra/compose/docker-compose.yml ps

# View live consolidated system logs
docker compose -f infra/compose/docker-compose.yml logs -f

# View logs for a specific service (e.g. gateway or addon)
docker compose -f infra/compose/docker-compose.yml logs -f gateway
```

### Updating JP to the Latest Version
```bash
cd /opt/jp
git pull origin main
./install.sh auto
```
*(Updates code and rebuilds containers without touching persistent volumes or database data).*

### Backups & Disaster Recovery
- All persistent data is stored in named Docker volumes (`postgres_data`, `redis_data`, `storage_data`, and `/var/lib/docker/volumes`).
- To trigger an automated snapshot backup, navigate to **Storage (`/storage`)** in the dashboard and click **Create Backup Snapshot**.

### Safe Uninstallation
If you ever want to uninstall JP:

- **Preserve All Data & Databases (Default):**
  ```bash
  cd /opt/jp
  ./uninstall.sh
  ```
  *(Removes containers while keeping all database tables, user apps, and volumes safely stored on disk).*

- **Full Server Teardown (Purge All Data):**
  ```bash
  cd /opt/jp
  ./uninstall.sh --purge-data
  ```

---

## 11. Troubleshooting & FAQs

### Q: Traefik is not issuing my Let's Encrypt SSL certificate?
- Ensure Port `80` and `443` are open in your firewall (`sudo ufw status`).
- Check that your DNS A Record has propagated (`dig +short panel.yourdomain.com` should output your server's IP).
- If using Cloudflare, set proxy status to **DNS Only** (grey cloud) during initial SSL issuance.

### Q: How do I test the system before putting it into production?
Run the built-in automated test suite:
- **On Linux / WSL:**
  ```bash
  bash scripts/test-all.sh
  ```
- **On Windows (PowerShell):**
  ```powershell
  powershell -ExecutionPolicy Bypass -File scripts\test-all.ps1
  ```
All 22 microservices and 240+ catalog apps are tested with realistic assertions.

### Q: How can I free up disk space?
Go to **System Settings (`/settings`)** in the Web Dashboard and click **Run Storage Cleanup (Prune)**. This safely deletes unused build caches, dangling intermediate images, and stopped ephemeral containers.
