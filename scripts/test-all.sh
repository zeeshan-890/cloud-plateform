#!/bin/bash
set -e
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "=== Running Go Test Suite across all modules ==="
MODULES=$(grep -E '^\s*\./' go.work | tr -d '\r \t')

for mod in $MODULES; do
    echo "============================================="
    echo "Testing module: $mod"
    echo "============================================="
    (cd "$ROOT_DIR/$mod" && go test -v ./...)
done

echo "============================================="
echo "All Go modules tested successfully!"
echo "============================================="
