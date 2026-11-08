#!/usr/bin/env bash
# Print real repository numbers (use these instead of copying figures into docs).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
count_lines() { find "$@" -print0 2>/dev/null | xargs -0 cat 2>/dev/null | wc -l; }
svc="$(find services -maxdepth 2 -name go.mod | wc -l)"
echo "go services (modules with go.mod):   $svc"
echo "go.work modules:                     $(awk '/^use \(/{u=1;next} u&&/^\)/{u=0} u&&NF' go.work | wc -l)"
echo "go lines (non-test):                 $(count_lines services packages apps/cli -name '*.go' ! -name '*_test.go')"
echo "go lines (tests):                    $(count_lines services packages apps/cli -name '*_test.go')"
echo "go test files:                       $(find services packages apps/cli -name '*_test.go' | wc -l)"
echo "dashboard ts/tsx lines:              $(count_lines apps/dashboard/src \( -name '*.ts' -o -name '*.tsx' \))"
echo "sql migrations:                      $(find services -path '*/migrations/*.sql' | wc -l)"
echo "gateway route registrations:         $(grep -cE 'mux\.Handle(Func)?\("(GET|POST|PUT|PATCH|DELETE) ' services/gateway/cmd/main.go)"
echo "gateway /api/v1 routes:              $(grep -cE 'mux\.Handle(Func)?\("(GET|POST|PUT|PATCH|DELETE) /api/v1' services/gateway/cmd/main.go)"
echo "addon catalog entries (DefaultImage): $(grep -c 'DefaultImage' services/addon/internal/catalog/catalog.go 2>/dev/null || echo 0)"
