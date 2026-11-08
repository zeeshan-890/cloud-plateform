#!/usr/bin/env bash
# Shared helpers for the operational scripts (source this file; do not execute).
# shellcheck shell=bash

JP_ROOT="${JP_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"

if [[ -t 1 ]]; then
  _c_red=$'\033[31m'; _c_grn=$'\033[32m'; _c_ylw=$'\033[33m'; _c_cyn=$'\033[36m'; _c_off=$'\033[0m'
else
  _c_red=""; _c_grn=""; _c_ylw=""; _c_cyn=""; _c_off=""
fi

log()  { printf '%s==>%s %s\n' "$_c_cyn" "$_c_off" "$*"; }
ok()   { printf '%s ok%s %s\n' "$_c_grn" "$_c_off" "$*"; }
warn() { printf '%swarn%s %s\n' "$_c_ylw" "$_c_off" "$*" >&2; }
err()  { printf '%serror%s %s\n' "$_c_red" "$_c_off" "$*" >&2; }
die()  { err "$*"; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# JP_DOCKER may be "docker" (default) or "sudo docker" when the current shell lacks docker group membership.
jp_docker_cmd() {
  if [[ -n "${JP_DOCKER:-}" ]]; then
    read -r -a JP_DOCKER_CMD <<<"$JP_DOCKER"
  elif docker info >/dev/null 2>&1; then
    JP_DOCKER_CMD=(docker)
  elif have sudo && sudo -n docker info >/dev/null 2>&1; then
    JP_DOCKER_CMD=(sudo docker)
  else
    JP_DOCKER_CMD=(docker)
  fi
}

# Read KEY=value from an env file (last wins, strips one pair of surrounding quotes).
env_get() {
  local key="$1" file="${2:-$JP_ROOT/.env}" line
  [[ -f "$file" ]] || return 1
  line="$(grep -E "^${key}=" "$file" | tail -n1)" || return 1
  line="${line#*=}"
  line="${line%\'}"; line="${line#\'}"; line="${line%\"}"; line="${line#\"}"
  printf '%s' "$line"
}

# Insert or update KEY=value in an env file (value is written verbatim; quote it yourself if it contains $).
env_set() {
  local key="$1" value="$2" file="${3:-$JP_ROOT/.env}" tmp
  if grep -qE "^${key}=" "$file" 2>/dev/null; then
    tmp="$(mktemp)"
    awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; next} {print}' "$file" >"$tmp"
    cat "$tmp" >"$file"
    rm -f "$tmp"
  else
    printf '%s=%s\n' "$key" "$value" >>"$file"
  fi
}

# Run docker compose against the production file + the overlays selected by the environment:
#   JP_DEV=1  adds docker-compose.dev.yml and infra/compose/dev.env defaults (local development only)
#   JP_E2E=1  adds docker-compose.e2e.yml (test DNS server)
# The sizing overlay comes from .env.resources (written by scripts/size-host.sh).
jp_compose() {
  jp_docker_cmd
  local args=(compose -f "$JP_ROOT/infra/compose/docker-compose.yml")
  local sizing=""
  if [[ -f "$JP_ROOT/.env.resources" ]]; then
    sizing="$(env_get COMPOSE_SIZING_FILE "$JP_ROOT/.env.resources" || true)"
  fi
  if [[ -n "$sizing" && -f "$JP_ROOT/$sizing" ]]; then args+=(-f "$JP_ROOT/$sizing"); fi
  if [[ -n "${JP_DEV:-}" ]]; then args+=(-f "$JP_ROOT/infra/compose/docker-compose.dev.yml"); fi
  if [[ -n "${JP_E2E:-}" ]]; then args+=(-f "$JP_ROOT/infra/compose/docker-compose.e2e.yml"); fi
  if [[ -n "${JP_DEV:-}" ]]; then args+=(--env-file "$JP_ROOT/infra/compose/dev.env"); fi
  if [[ -f "$JP_ROOT/.env" ]]; then args+=(--env-file "$JP_ROOT/.env"); fi
  if [[ -f "$JP_ROOT/.env.resources" ]]; then args+=(--env-file "$JP_ROOT/.env.resources"); fi
  (cd "$JP_ROOT" && "${JP_DOCKER_CMD[@]}" "${args[@]}" "$@")
}

# Wait until every container of the stack that defines a healthcheck is healthy (or $1 seconds elapse).
jp_wait_healthy() {
  local timeout="${1:-300}" waited=0 bad=""
  while ((waited < timeout)); do
    bad="$(jp_compose ps --format '{{.Service}} {{.Health}} {{.State}}' 2>/dev/null |
      awk '$2=="starting"||$2=="unhealthy"||($3!="running"&&$3!="exited"){print $1}' | tr '\n' ' ')"
    if [[ -z "${bad// /}" ]]; then return 0; fi
    sleep 3
    waited=$((waited + 3))
  done
  warn "still not healthy after ${timeout}s: ${bad}"
  return 1
}
