#!/usr/bin/env bash
# JP Cloud - uninstaller.
#
#   ./uninstall.sh                 stop + remove platform containers, tenant containers/networks. KEEPS all data volumes and .env
#   ./uninstall.sh --purge-data    also delete volumes (database, MinIO, registry, certificates, tenant add-on data) and .env
#   ./uninstall.sh --remove-images also remove the locally built jp-* images
#   ./uninstall.sh -y              no confirmation prompt
#
# Tenant containers/networks are identified by the label jp.managed=true (created by the runtime service) and the
# networks named jp_p_*; they are removed in every mode because they cannot run without the platform.
# Back up first if unsure: scripts/backup.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"

PURGE=0 IMAGES=0 YES=0
for a in "$@"; do
  case "$a" in
    --purge-data) PURGE=1 ;;
    --remove-images) IMAGES=1 ;;
    -y|--yes) YES=1 ;;
    -h|--help) sed -n '2,11p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $a" ;;
  esac
done

have docker || die "docker not found"
jp_docker_cmd

if ((PURGE && !YES)); then
  warn "--purge-data PERMANENTLY DELETES the database, storage buckets, certificates, add-on data and .env (including SECRETS_MASTER_KEY)."
  read -r -p "Type 'delete everything' to continue: " ans
  [[ "$ans" == "delete everything" ]] || die "aborted"
elif ((!YES)); then
  read -r -p "Stop and remove the platform and all tenant containers? Data volumes are kept. [y/N] " ans
  [[ "$ans" =~ ^[Yy]$ ]] || die "aborted"
fi

log "removing tenant containers (label jp.managed=true)"
mapfile -t tc < <("${JP_DOCKER_CMD[@]}" ps -aq --filter label=jp.managed=true)
((${#tc[@]})) && "${JP_DOCKER_CMD[@]}" rm -f "${tc[@]}" >/dev/null

log "stopping the platform stack"
DOWN=(down --remove-orphans)
if ((PURGE)); then DOWN+=(--volumes); fi
JP_E2E=1 jp_compose --profile '*' "${DOWN[@]}" 2>/dev/null || jp_compose --profile '*' "${DOWN[@]}" || true

log "removing tenant networks (jp_p_*)"
for n in $("${JP_DOCKER_CMD[@]}" network ls -q --filter name=jp_p_); do
  "${JP_DOCKER_CMD[@]}" network rm "$n" >/dev/null 2>&1 || warn "network $n still in use"
done

if ((PURGE)); then
  log "removing tenant volumes"
  for v in $("${JP_DOCKER_CMD[@]}" volume ls -q --filter label=jp.managed=true) $("${JP_DOCKER_CMD[@]}" volume ls -q --filter name=jp-addon-) $("${JP_DOCKER_CMD[@]}" volume ls -q --filter name=jp-db-); do
    "${JP_DOCKER_CMD[@]}" volume rm "$v" >/dev/null 2>&1 || true
  done
  rm -f "$ROOT/.env" "$ROOT/.env.resources" "$ROOT/.env.pre-restore"
  ok "all data and configuration removed"
else
  ok "containers removed; volumes and .env kept. Start again with ./install.sh or scripts/jp-up.sh"
fi

if ((IMAGES)); then
  log "removing built images"
  mapfile -t imgs < <("${JP_DOCKER_CMD[@]}" images --format '{{.Repository}}:{{.Tag}}' | grep -E '^jp-' || true)
  ((${#imgs[@]})) && "${JP_DOCKER_CMD[@]}" rmi -f "${imgs[@]}" >/dev/null || true
fi
echo "Firewall rules, Docker itself and ${ROOT} are left untouched."
