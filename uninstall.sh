#!/usr/bin/env bash
# ==============================================================================
# JP Cloud Platform — Canonical Uninstaller
# Gracefully stops platform services. Preserves persistent data by default
# unless --purge-data is explicitly specified.
#
# Usage:
#   ./uninstall.sh                # Preserves all databases and volume data
#   ./uninstall.sh --purge-data   # Completely removes data volumes and .env
# ==============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT"

PURGE_DATA=false
FORCE=false

for arg in "$@"; do
  case "$arg" in
    --purge-data)
      PURGE_DATA=true
      ;;
    -y|--yes)
      FORCE=true
      ;;
    *)
      echo "Unknown flag: $arg"
      echo "Usage: ./uninstall.sh [--purge-data] [-y|--yes]"
      exit 1
      ;;
  esac
done

echo "=========================================================="
echo "    JP Cloud Platform — Uninstaller"
echo "=========================================================="
echo ""

if [[ "$PURGE_DATA" == "true" && "$FORCE" == "false" ]]; then
  echo "WARNING: You have specified --purge-data."
  echo "This will PERMANENTLY DELETE all database volumes, user data, and configuration!"
  read -r -p "Are you sure you want to proceed? [y/N]: " CONFIRM
  if [[ ! "$CONFIRM" =~ ^[Yy]$ ]]; then
    echo "Uninstall aborted."
    exit 0
  fi
fi

echo "==> Stopping JP containers and services..."
if command -v docker >/dev/null 2>&1; then
  if [[ -f infra/compose/docker-compose.yml ]]; then
    docker compose -f infra/compose/docker-compose.yml down --remove-orphans || true
  fi
fi

if [[ "$PURGE_DATA" == "true" ]]; then
  echo "==> Purging all persistent Docker volumes..."
  if command -v docker >/dev/null 2>&1; then
    docker volume ls -q | grep -E "^(compose_|jp_)" | xargs -r docker volume rm || true
    docker compose -f infra/compose/docker-compose.yml down -v --remove-orphans 2>/dev/null || true
  fi

  echo "==> Removing environment configuration..."
  rm -f .env .env.resources
  echo "All platform data and volumes have been permanently removed."
else
  echo ""
  echo "=========================================================="
  echo "  JP platform containers have been stopped and removed."
  echo "  Persistent volumes (databases, storage, keys) PRESERVED."
  echo "=========================================================="
  echo ""
  echo "  To start the platform again: ./install.sh"
  echo "  To completely remove all data: ./uninstall.sh --purge-data"
fi
