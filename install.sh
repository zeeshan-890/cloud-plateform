#!/usr/bin/env bash
# ==============================================================================
# JP Cloud Platform — Canonical Single-Server Installer
# Installs JP, verifies prerequisites, adapts sizing, and boots the platform.
#
# Usage:
#   curl -fsSL https://get.jpcloud.dev | bash
#   or locally: ./install.sh [lite|small|medium|large|xlarge|auto]
# ==============================================================================
set -euo pipefail

TIER="${1:-auto}"
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

echo "=========================================================="
echo "    JP Cloud Platform — Self-Hosted Installation"
echo "=========================================================="
echo ""

# 1. Check OS
OS="$(uname -s)"
if [[ "$OS" != "Linux" && "$OS" != "Darwin" ]]; then
  echo "ERROR: Unsupported operating system: $OS. Linux or macOS required." >&2
  exit 1
fi

# 2. Check Prerequisites
echo "==> Validating prerequisites..."
command -v docker >/dev/null 2>&1 || {
  echo "ERROR: Docker is not installed. Please install Docker Engine 24.0+." >&2
  exit 1
}

docker compose version >/dev/null 2>&1 || {
  echo "ERROR: Docker Compose V2 is required ('docker compose')." >&2
  exit 1
}

# 3. Check Disk Space (minimum 5GB free)
FREE_KB="$(df -k "$ROOT" | awk 'NR==2 {print $4}')"
if [[ "$FREE_KB" -lt 5242880 ]]; then
  echo "WARNING: Less than 5 GB free disk space available. Builds may fail if space runs out." >&2
fi

# 4. Hardware Sizing
echo "==> Detecting host capacity and calculating resource overlay..."
"$ROOT/scripts/size-host.sh" "$TIER"

# 5. Environment & Secrets Generation
if [[ ! -f .env ]]; then
  echo "==> Generating secure cryptographically random platform secrets..."
  cp .env.example .env

  JWT_SEC="$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)"
  MASTER_SEC="$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 32)"
  DB_PASS="$(head -c 16 /dev/urandom | hex 2>/dev/null || head -c 16 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)"

  sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=${JWT_SEC}|" .env
  sed -i.bak "s|^SECRETS_MASTER_KEY=.*|SECRETS_MASTER_KEY=${MASTER_SEC}|" .env
  sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${DB_PASS}|" .env
  sed -i.bak "s|^DATABASE_URL=.*|DATABASE_URL=postgres://jp:${DB_PASS}@postgres:5432/jp?sslmode=disable|" .env
  rm -f .env.bak
  echo "    Generated JWT_SECRET, SECRETS_MASTER_KEY, and database credentials."
fi

# 6. Launch Platform
echo "==> Starting platform services with Docker Compose..."
"$ROOT/scripts/jp-up.sh" "" "$TIER"

# Load assigned resources & ports
if [[ -f .env.resources ]]; then
  # shellcheck source=/dev/null
  source .env.resources
fi
GW_PORT="${GATEWAY_PORT:-8000}"
UI_PORT="${DASHBOARD_PORT:-3000}"

# 7. Wait for Gateway Healthcheck
echo "==> Waiting for API Gateway readiness on port ${GW_PORT}..."
READY=false
for i in {1..30}; do
  if curl -sf "http://localhost:${GW_PORT}/healthz" >/dev/null 2>&1; then
    READY=true
    break
  fi
  sleep 2
done

echo ""
if [[ "$READY" == "true" ]]; then
  echo "=========================================================="
  echo "  SUCCESS! JP Cloud Platform is online and healthy."
  echo "=========================================================="
  echo ""
  echo "  Web Console:   http://localhost:${UI_PORT}"
  echo "  API Gateway:   http://localhost:${GW_PORT}"
  echo ""
  echo "  Next Steps:"
  echo "  1. Open http://localhost:${UI_PORT} in your browser to create your administrator account."
  echo "  2. Go to 'Domains' to attach your management domain (e.g. panel.yourdomain.com)."
  echo "  3. Let's Encrypt will automatically issue an HTTPS certificate for your domain."
  echo ""
else
  echo "WARNING: Gateway did not respond within 60 seconds on port ${GW_PORT}."
  echo "Check container status: docker compose ps"
  echo "Inspect logs: docker compose logs -f gateway"
fi
