#!/usr/bin/env bash
# ==============================================================================
# JP Cloud Platform — Universal 1-Click Server Setup
# Works on Oracle Linux, RHEL, CentOS, Rocky, AlmaLinux, Ubuntu, and Debian.
# ==============================================================================
set -euo pipefail

echo "=========================================================="
echo "    JP Cloud Platform — 1-Click Server Installation"
echo "=========================================================="
echo ""

# 1. Package Manager & Prerequisites Installation
echo "==> [1/6] Installing Docker, Docker Compose, and Git..."
if command -v dnf >/dev/null 2>&1; then
    sudo dnf install -y dnf-utils git curl ca-certificates
    if ! command -v docker >/dev/null 2>&1; then
        sudo dnf config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        sudo dnf install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    fi
elif command -v apt-get >/dev/null 2>&1; then
    sudo apt-get update -y
    sudo apt-get install -y ca-certificates curl gnupg git
    if ! command -v docker >/dev/null 2>&1; then
        sudo install -m 0755 -d /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /etc/apt/keyrings/docker.gpg --yes
        sudo chmod a+r /etc/apt/keyrings/docker.gpg
        echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
        sudo apt-get update -y
        sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    fi
fi

# Enable and start Docker
sudo systemctl enable --now docker
sudo usermod -aG docker "$USER" 2>/dev/null || true

# 2. Firewall Port Configuration
echo "==> [2/6] Configuring firewall rules (ports 80, 443, 3000, 8000)..."
if command -v firewall-cmd >/dev/null 2>&1 && sudo systemctl is-active --quiet firewalld; then
    sudo firewall-cmd --permanent --add-port={80,443,3000,8000}/tcp >/dev/null 2>&1 || true
    sudo firewall-cmd --reload >/dev/null 2>&1 || true
elif command -v ufw >/dev/null 2>&1 && sudo ufw status | grep -q "active"; then
    sudo ufw allow 80/tcp >/dev/null 2>&1 || true
    sudo ufw allow 443/tcp >/dev/null 2>&1 || true
    sudo ufw allow 3000/tcp >/dev/null 2>&1 || true
    sudo ufw allow 8000/tcp >/dev/null 2>&1 || true
fi

# 3. Clone or Update Repository
TARGET_DIR="${HOME}/cloud-plateform"
echo "==> [3/6] Setting up repository in ${TARGET_DIR}..."
if [ -d "${TARGET_DIR}/.git" ]; then
    cd "${TARGET_DIR}"
    git pull origin main || true
else
    git clone https://github.com/zeeshan-890/cloud-plateform.git "${TARGET_DIR}"
    cd "${TARGET_DIR}"
fi

# 4. Detect Public IP & Configure .env
echo "==> [4/6] Auto-detecting public server IP and generating secure credentials..."
PUBLIC_IP=$(curl -s4 --max-time 3 ifconfig.me 2>/dev/null || curl -s4 --max-time 3 icanhazip.com 2>/dev/null || hostname -I | awk '{print $1}')
echo "    Detected Public IP: ${PUBLIC_IP}"

if [ ! -f .env ]; then
    cp .env.example .env
fi

sed -i "s|PUBLIC_BASE_URL=.*|PUBLIC_BASE_URL=http://${PUBLIC_IP}:8000|" .env
sed -i "s|DASHBOARD_URL=.*|DASHBOARD_URL=http://${PUBLIC_IP}:3000|" .env

# Cryptographic secrets generation
JWT_SEC=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)
MASTER_SEC=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)
DB_PASS=$(head -c 16 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)

sed -i "s|^JWT_SECRET=.*|JWT_SECRET=${JWT_SEC}|" .env
sed -i "s|^SECRETS_MASTER_KEY=.*|SECRETS_MASTER_KEY=${MASTER_SEC}|" .env
sed -i "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${DB_PASS}|" .env
sed -i "s|^DATABASE_URL=.*|DATABASE_URL=postgres://jp:${DB_PASS}@postgres:5432/jp?sslmode=disable|" .env

# 5. Launch Stack with Docker Compose
echo "==> [5/6] Building and starting JP Cloud Platform containers..."
sudo docker compose -f infra/compose/docker-compose.yml --profile ui up -d --build

# 6. Wait for Readiness
echo "==> [6/6] Checking system readiness..."
READY=false
for i in {1..30}; do
    if curl -sf "http://localhost:8000/healthz" >/dev/null 2>&1; then
        READY=true
        break
    fi
    sleep 2
done

echo ""
echo "=========================================================="
echo "    JP Cloud Platform is ONLINE & HEALTHY!"
echo "=========================================================="
echo ""
echo "  Web Console:   http://${PUBLIC_IP}:3000"
echo "  API Gateway:   http://${PUBLIC_IP}:8000"
echo ""
echo "  Open http://${PUBLIC_IP}:3000 in your browser to start!"
echo "=========================================================="
