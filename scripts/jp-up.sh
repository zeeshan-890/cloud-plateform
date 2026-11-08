#!/usr/bin/env bash
# Start (or update) the stack with the right compose files for this host.
#
#   scripts/jp-up.sh                    production stack, sizing overlay from scripts/size-host.sh
#   scripts/jp-up.sh --dev              + docker-compose.dev.yml and dev.env defaults (local development only)
#   scripts/jp-up.sh --monitoring       enable prometheus/grafana/loki/tempo/promtail (persisted in .env COMPOSE_PROFILES)
#   scripts/jp-up.sh --no-monitoring    disable them again
#   scripts/jp-up.sh --e2e              + test DNS server (docker-compose.e2e.yml)
#   scripts/jp-up.sh --size medium      force a sizing tier (default: auto)
#   scripts/jp-up.sh --pull             use the prebuilt images published for this commit (ghcr.io); build only what is missing
#   scripts/jp-up.sh --no-build         do not rebuild images
#   scripts/jp-up.sh --wait             wait until every container is healthy
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"

SIZE=auto
BUILD=1
WAIT=0
PULL=0
MON=""
while (($#)); do
  case "$1" in
    --dev) export JP_DEV=1 ;;
    --e2e) export JP_E2E=1 ;;
    --monitoring) MON=on ;;
    --no-monitoring) MON=off ;;
    --size) SIZE="${2:?--size needs a value}"; shift ;;
    --no-build) BUILD=0 ;;
    --pull) PULL=1 ;;
    --wait) WAIT=1 ;;
    -h|--help) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (see --help)" ;;
  esac
  shift
done

cd "$ROOT"
if [[ ! -f .env && -z "${JP_DEV:-}" ]]; then
  die ".env not found - run ./install.sh first (or use --dev for local development)"
fi
"$ROOT/scripts/size-host.sh" "$SIZE"

if [[ -n "$MON" && -f .env ]]; then
  profiles="$(env_get COMPOSE_PROFILES || true)"
  if [[ "$MON" == on ]]; then
    [[ ",$profiles," == *",monitoring,"* ]] || profiles="${profiles:+$profiles,}monitoring"
    if [[ -z "$(env_get GRAFANA_ADMIN_PASSWORD || true)" ]]; then
      env_set GRAFANA_ADMIN_PASSWORD "$(openssl rand -hex 12)"
      log "generated GRAFANA_ADMIN_PASSWORD in .env"
    fi
  else
    profiles="$(printf '%s' ",$profiles," | sed 's/,monitoring,/,/; s/^,//; s/,$//')"
  fi
  env_set COMPOSE_PROFILES "$profiles"
fi
if [[ -n "${JP_DEV:-}" && -z "$MON" ]]; then export COMPOSE_PROFILES="${COMPOSE_PROFILES:-dev}"; fi

if ((PULL)) && [[ -f .env ]] && have git && [[ -d .git ]]; then
  # Images are published per commit (tag sha-<7 chars>). Pull the ones for this checkout; anything not published (yet, or on a
  # fork) is simply built from source below, so this never makes an install fail.
  tag="sha-$(git rev-parse --short=7 HEAD)"
  env_set JP_IMAGE_TAG "$tag"
  log "pulling prebuilt images ($tag)"
  if jp_compose pull --quiet --ignore-pull-failures 2>/dev/null; then BUILD=0; fi
fi

REPLICAS="$(env_get WORKER_REPLICAS "$ROOT/.env.resources" || echo 1)"
UP=(up -d --remove-orphans)
if ((BUILD)); then UP+=(--build); fi
if ((REPLICAS > 1)); then UP+=(--scale "worker=$REPLICAS"); fi

log "docker compose ${UP[*]}"
jp_compose "${UP[@]}"
if ((WAIT)); then jp_wait_healthy 300 || exit 1; fi
ok "stack is up (size=$(env_get JP_SIZE "$ROOT/.env.resources"), workers x$REPLICAS)"
