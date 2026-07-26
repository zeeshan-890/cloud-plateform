#!/usr/bin/env bash
# Switch jp to REAL modes and bring the stack up.
# Usage: ./scripts/go-real.sh [profiles] [size]
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
PROFILES="${1:-platform,ui,data}"
SIZE="${2:-auto}"

echo "==> Enabling REAL platform modes"

[[ -f .env ]] || cp .env.example .env

set_key() {
  local file="$1" key="$2" val="$3"
  if grep -q "^${key}=" "$file" 2>/dev/null; then
    sed -i.bak "s|^${key}=.*|${key}=${val}|" "$file" && rm -f "${file}.bak"
  else
    echo "${key}=${val}" >> "$file"
  fi
}

for f in .env; do
  set_key "$f" BUILD_MODE docker
  set_key "$f" SIMULATE_BUILD false
  set_key "$f" RUNTIME_MODE docker
  set_key "$f" STORAGE_MODE minio
  set_key "$f" DB_MODE schema
  set_key "$f" METRICS_MODE live
  set_key "$f" DOMAIN_DNS_STUB false
  set_key "$f" CERT_SIMULATE false
  set_key "$f" REGISTRY_PUSH_URL host.docker.internal:5000
  set_key "$f" REGISTRY_HOST host.docker.internal:5000
done

"$ROOT/scripts/size-host.sh" "$SIZE"

set_key .env.resources BUILD_MODE docker
set_key .env.resources SIMULATE_BUILD false
set_key .env.resources RUNTIME_MODE docker
set_key .env.resources METRICS_MODE live
set_key .env.resources STORAGE_MODE minio
set_key .env.resources DB_MODE schema

echo ""
echo "If image push fails, allow insecure registry localhost:5000 in Docker daemon.json"
echo ""

"$ROOT/scripts/jp-up.sh" "$PROFILES" "$SIZE"

echo ""
echo "REAL mode stack requested. Verify: curl http://localhost:8000/healthz"
