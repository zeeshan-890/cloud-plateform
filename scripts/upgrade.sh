#!/usr/bin/env bash
# Upgrade an installed platform in place, with automatic rollback when a service does not become healthy.
#
#   scripts/upgrade.sh [--ref BRANCH_OR_TAG] [--no-pull] [--no-backup] [--prune]
#
#   1. backup (scripts/backup.sh) unless --no-backup
#   2. git pull --ff-only (or checkout --ref)
#   3. tags the currently running images as :previous, builds the new ones
#   4. restarts services one by one in dependency order and waits for each healthcheck
#   5. on any failure: checks out the previous git revision, re-tags :previous as the running image, recreates the
#      services and exits non-zero. Previous images are always kept (until --prune removes dangling layers).
#
# Database migrations run when a service starts and are forward-only: a rollback restores old CODE, not old SCHEMA.
# If a migration changed the schema incompatibly, restore the backup taken in step 1 with scripts/restore.sh.
# Tenant application containers are separate containers and keep running during the upgrade.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"

REF="" PULL=1 BACKUP=1 PRUNE=0
while (($#)); do
  case "$1" in
    --ref) REF="${2:?}"; shift ;;
    --no-pull) PULL=0 ;;
    --no-backup) BACKUP=0 ;;
    --prune) PRUNE=1 ;;
    -h|--help) sed -n '2,15p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
  shift
done
cd "$ROOT"
[[ -f .env ]] || die ".env not found"
jp_docker_cmd

# order: infra -> data plane -> control plane -> edge. Anything not listed is appended afterwards.
ORDER=(docker-socket-proxy postgres redis registry minio identity organization project notification secret
       repository build deployment registry-api runtime scheduler domain certificate logging metrics storage database addon
       ai worker gateway dashboard traefik)

PREV_REV="$(git rev-parse HEAD)"
if ((BACKUP)); then
  log "backup before upgrade"
  "$ROOT/scripts/backup.sh" --quiet --keep 5 >/dev/null || die "backup failed; fix it or pass --no-backup"
fi

if ((PULL)); then
  log "fetching code"
  git fetch --quiet origin
  if [[ -n "$REF" ]]; then git checkout --quiet "$REF"; fi
  git pull --ff-only --quiet || warn "git pull skipped (detached or diverged)"
fi
NEW_REV="$(git rev-parse HEAD)"
if [[ "$NEW_REV" == "$PREV_REV" ]]; then warn "no new commits; rebuilding anyway"; fi

mapfile -t ALL < <(jp_compose config --services)
SERVICES=()
for s in "${ORDER[@]}"; do for a in "${ALL[@]}"; do [[ "$a" == "$s" ]] && SERVICES+=("$s"); done; done
for a in "${ALL[@]}"; do [[ " ${SERVICES[*]} " == *" $a "* ]] || SERVICES+=("$a"); done

# images built from this repo are named jp-<service>
tag_previous() {
  for s in "${SERVICES[@]}"; do
    if "${JP_DOCKER_CMD[@]}" image inspect "jp-$s:latest" >/dev/null 2>&1; then
      "${JP_DOCKER_CMD[@]}" tag "jp-$s:latest" "jp-$s:previous"
    fi
  done
}
rollback() {
  err "upgrade failed - rolling back to $PREV_REV"
  git checkout --quiet "$PREV_REV" 2>/dev/null || warn "could not check out $PREV_REV; restore it manually"
  for s in "${SERVICES[@]}"; do
    if "${JP_DOCKER_CMD[@]}" image inspect "jp-$s:previous" >/dev/null 2>&1; then
      "${JP_DOCKER_CMD[@]}" tag "jp-$s:previous" "jp-$s:latest"
    fi
  done
  jp_compose up -d --no-build --remove-orphans || true
  warn "repo is now at $PREV_REV (detached). Fix the problem, then: git checkout main && scripts/upgrade.sh"
  warn "if the schema was migrated incompatibly, restore the pre-upgrade backup: scripts/restore.sh backups/<latest>"
  exit 1
}

log "tagging running images as :previous"
tag_previous
log "building new images"
jp_compose build --pull || rollback

wait_healthy() { # wait_healthy <service> <timeout>
  local svc="$1" t="${2:-150}" w=0 st
  while ((w < t)); do
    st="$(jp_compose ps --format '{{.Service}} {{.State}} {{.Health}}' "$svc" 2>/dev/null | awk '{print $2" "$3}' | head -n1)"
    case "$st" in
      "running healthy"|"running ") return 0 ;;
      "exited "*|"dead "*) return 1 ;;
    esac
    sleep 3; w=$((w + 3))
  done
  return 1
}

for s in "${SERVICES[@]}"; do
  log "restarting $s"
  jp_compose up -d --no-deps --no-build "$s" >/dev/null || rollback
  wait_healthy "$s" 150 || { err "$s did not become healthy"; jp_compose logs --tail=40 "$s" || true; rollback; }
done

GW="$(env_get GATEWAY_PORT || echo 8000)"; GW="${GW:-8000}"
curl -fsS --max-time 5 "http://127.0.0.1:${GW}/healthz" >/dev/null || { err "gateway /healthz failed"; rollback; }

if ((PRUNE)); then "${JP_DOCKER_CMD[@]}" image prune -f --filter "until=168h" >/dev/null; fi
ok "upgraded $(git rev-parse --short "$PREV_REV") -> $(git rev-parse --short HEAD); previous images kept as jp-<service>:previous"
