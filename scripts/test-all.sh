#!/usr/bin/env bash
# Run vet + race tests for every module in go.work (the same loop CI uses).
# `go test ./services/...` from the repo root does NOT work with go.work: patterns must match modules in the workspace
# and each service is its own module, so we iterate over the `use` entries instead.
#   scripts/test-all.sh            vet + test -race
#   scripts/test-all.sh --short    skip -race (faster, no CGO needed)
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
RACE=(-race)
if [[ "${1:-}" == "--short" ]]; then RACE=(); fi

mapfile -t MODULES < <(awk '/^use \(/{u=1;next} u&&/^\)/{u=0} u{gsub(/[ \t\r]/,""); if ($0!="") print}' go.work)
fail=0
for m in "${MODULES[@]}"; do
  echo "=== $m"
  (cd "$ROOT/$m" && go vet ./... && go test "${RACE[@]}" -count=1 ./...) || { echo "FAILED: $m" >&2; fail=1; }
done
exit "$fail"
