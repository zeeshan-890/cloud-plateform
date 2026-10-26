#!/usr/bin/env bash
# ==============================================================================
# Helper to find a free, unallocated port starting from a default port.
# Usage: ./scripts/find-free-port.sh <default_port>
# ==============================================================================
set -euo pipefail

is_port_busy() {
  local port="$1"

  # 1. Check ss (modern Linux socket statistics)
  if command -v ss >/dev/null 2>&1; then
    if ss -tuln 2>/dev/null | grep -qE "[:\.]${port}[[:space:]]"; then
      return 0
    fi
  fi

  # 2. Check netstat if available
  if command -v netstat >/dev/null 2>&1; then
    if netstat -tuln 2>/dev/null | grep -qE "[:\.]${port}[[:space:]]"; then
      return 0
    fi
  fi

  # 3. Check lsof if available
  if command -v lsof >/dev/null 2>&1; then
    if lsof -iTCP:"${port}" -sTCP:LISTEN >/dev/null 2>&1; then
      return 0
    fi
  fi

  # 4. Check Docker container port mappings
  if command -v docker >/dev/null 2>&1; then
    if docker ps --format '{{.Ports}}' 2>/dev/null | grep -qE "(:|\])${port}->"; then
      return 0
    fi
  fi

  return 1
}

find_free() {
  local port="${1:-80}"
  while is_port_busy "$port"; do
    port=$((port + 1))
  done
  echo "$port"
}

find_free "${1:-80}"
