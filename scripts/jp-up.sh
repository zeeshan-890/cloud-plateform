#!/usr/bin/env bash
# Starts jp with host-aware resource overlay + worker scale.
# Usage: ./scripts/jp-up.sh [profiles] [size]
#   ./scripts/jp-up.sh
#   ./scripts/jp-up.sh platform,ui,data
#   ./scripts/jp-up.sh platform,ui,data,monitoring large
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PROFILES_ARG="${1:-}"
SIZE="${2:-auto}"

"$ROOT/scripts/size-host.sh" "$SIZE"

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo "created .env from .env.example"
fi

# shellcheck disable=SC1091
set -a
# shellcheck source=/dev/null
source .env.resources
set +a

TIER="${JP_SIZE}"
OVERLAY="${COMPOSE_SIZING_FILE}"
REPLICAS="${WORKER_REPLICAS:-1}"
PROFILES="${PROFILES_ARG:-$JP_SUGGESTED_PROFILES}"

ARGS=(-f infra/compose/docker-compose.yml -f "$OVERLAY" --env-file .env --env-file .env.resources)
IFS=',' read -ra PARTS <<< "$PROFILES"
for p in "${PARTS[@]}"; do
  p="$(echo "$p" | xargs)"
  [[ -n "$p" && "$p" != "core" ]] && ARGS+=(--profile "$p")
done

UP=(up -d --build)
[[ "$REPLICAS" -gt 1 ]] && UP+=(--scale "worker=$REPLICAS")

echo "docker compose ${ARGS[*]} ${UP[*]}"
docker compose "${ARGS[@]}" "${UP[@]}"

echo ""
echo "jp is up — size=$TIER workers×$REPLICAS profiles=$PROFILES"
echo "API: http://localhost:8000/api/v1"
