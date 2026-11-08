#!/usr/bin/env bash
# Restore a backup made by scripts/backup.sh.
#
#   scripts/restore.sh <backup-dir> [--yes] [--restore-env] [--with-tenant-volumes]
#
# What it does (DESTRUCTIVE for the current platform data):
#   1. verifies MANIFEST.sha256 and the pg archive
#   2. refuses to continue if the backup's SECRETS_MASTER_KEY differs from the current .env (stored secrets would be
#      unreadable) unless --restore-env is given, which puts the backup's .env in place (the old one is kept as .env.pre-restore)
#   3. stops every service except postgres, drops + recreates the database, pg_restore
#   4. restores the MinIO volume and Traefik state
#   5. starts the stack again and waits for the gateway to be healthy
# Tenant application containers are not part of a backup: redeploy projects afterwards (add-on volumes are restored only
# with --with-tenant-volumes and only if the backup contains them).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"

SRC="" YES=0 RESTORE_ENV=0 TENANT=0
while (($#)); do
  case "$1" in
    --yes|-y) YES=1 ;;
    --restore-env) RESTORE_ENV=1 ;;
    --with-tenant-volumes) TENANT=1 ;;
    -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) die "unknown option: $1" ;;
    *) SRC="$1" ;;
  esac
  shift
done
[[ -n "$SRC" && -d "$SRC" ]] || die "usage: restore.sh <backup-dir> [--yes] [--restore-env]"
SRC="$(cd "$SRC" && pwd)"
jp_docker_cmd

log "verifying backup integrity"
(cd "$SRC" && sha256sum -c --quiet MANIFEST.sha256) || die "checksum mismatch - backup is damaged"
[[ -f "$SRC/postgres.dump" ]] || die "postgres.dump missing"

# --- env / master key check
BK_ENV="$SRC/env.backup"
if [[ -f "$SRC/env.backup.enc" ]]; then
  [[ -n "${BACKUP_PASSPHRASE:-}" ]] || die "backup env is encrypted: export BACKUP_PASSPHRASE"
  BK_ENV="$(mktemp)"; trap 'rm -f "$BK_ENV"' EXIT
  openssl enc -d -aes-256-cbc -pbkdf2 -pass env:BACKUP_PASSPHRASE -in "$SRC/env.backup.enc" -out "$BK_ENV"
fi
bk_key="$(env_get SECRETS_MASTER_KEY "$BK_ENV" || true)"
cur_key="$(env_get SECRETS_MASTER_KEY "$ROOT/.env" || true)"
if ((RESTORE_ENV == 0)) && [[ -f "$ROOT/.env" && -n "$bk_key" && "$bk_key" != "$cur_key" ]]; then
  die "SECRETS_MASTER_KEY in the backup differs from the current .env: restored secrets would be unreadable. Re-run with --restore-env."
fi

if ((YES == 0)); then
  warn "This REPLACES the platform database and MinIO data of this server with the backup from $(basename "$SRC")."
  read -r -p "Type 'restore' to continue: " ans
  [[ "$ans" == "restore" ]] || die "aborted"
fi

if ((RESTORE_ENV)); then
  [[ -f "$ROOT/.env" ]] && cp -p "$ROOT/.env" "$ROOT/.env.pre-restore"
  install -m 600 "$BK_ENV" "$ROOT/.env"
  ok ".env restored from backup (previous one: .env.pre-restore)"
fi
PG_USER="$(env_get POSTGRES_USER || echo jp)"; PG_DB="$(env_get POSTGRES_DB || echo jp)"

log "stopping services (keeping postgres)"
mapfile -t svcs < <(jp_compose ps --services 2>/dev/null | grep -vx -e postgres -e init-volumes || true)
((${#svcs[@]})) && jp_compose stop "${svcs[@]}" >/dev/null
jp_compose up -d postgres >/dev/null
for _ in $(seq 1 30); do jp_compose exec -T postgres pg_isready -U "$PG_USER" >/dev/null 2>&1 && break; sleep 2; done

log "restoring database"
jp_compose exec -T postgres psql -U "$PG_USER" -d postgres -v ON_ERROR_STOP=1 \
  -c "DROP DATABASE IF EXISTS \"$PG_DB\" WITH (FORCE)" -c "CREATE DATABASE \"$PG_DB\" OWNER \"$PG_USER\""
jp_compose exec -T postgres pg_restore -U "$PG_USER" -d "$PG_DB" --no-owner --exit-on-error <"$SRC/postgres.dump"

restore_volume() { # restore_volume <volume> <tarball>
  [[ -f "$2" ]] || { warn "$(basename "$2") not in backup, skipping $1"; return 0; }
  "${JP_DOCKER_CMD[@]}" volume create "$1" >/dev/null
  "${JP_DOCKER_CMD[@]}" run --rm -v "$1":/data -v "$(dirname "$2")":/in:ro busybox:1.37 \
    sh -c "find /data -mindepth 1 -delete && tar xzf /in/$(basename "$2") -C /data"
}
log "restoring MinIO and Traefik state"
restore_volume jp_minio_data "$SRC/minio.tar.gz"
restore_volume jp_traefik_acme "$SRC/acme.tar.gz"
restore_volume jp_traefik_dynamic "$SRC/traefik_dynamic.tar.gz"
if ((TENANT)) && [[ -d "$SRC/tenant-volumes" ]]; then
  for f in "$SRC"/tenant-volumes/*.tar.gz; do
    [[ -f "$f" ]] || continue
    v="$(basename "$f" .tar.gz)"; log "tenant volume $v"; restore_volume "$v" "$f"
  done
fi

log "starting the stack"
"$ROOT/scripts/jp-up.sh" --no-build >/dev/null
GW="$(env_get GATEWAY_PORT || echo 8000)"; GW="${GW:-8000}"
for _ in $(seq 1 60); do
  if curl -fsS --max-time 3 "http://127.0.0.1:${GW}/healthz" >/dev/null 2>&1; then ok "restore complete, gateway healthy"; exit 0; fi
  sleep 3
done
die "restore finished but the gateway is not healthy: scripts/jp-compose.sh logs --tail=80 gateway"
