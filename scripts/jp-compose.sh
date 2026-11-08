#!/usr/bin/env bash
# Run `docker compose` with exactly the files/env this install uses.
#   scripts/jp-compose.sh ps
#   scripts/jp-compose.sh logs -f --tail=100 gateway
#   JP_DEV=1 scripts/jp-compose.sh ps          (development stack)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=scripts/lib.sh
source "$ROOT/scripts/lib.sh"
jp_compose "$@"
