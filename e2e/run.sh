#!/usr/bin/env bash
# Run the JP Cloud black-box end-to-end suite against a deployed platform.
#
#   E2E_BASE_URL=http://161.118.168.6 E2E_SSH=oracle-2nd ./run.sh            # everything
#   ./run.sh -run 'TestAuth|TestUsers'                                        # subset (extra args go to `go test`)
#
# See README.md for the full list of environment variables.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")"

export GOWORK=off
export E2E_BASE_URL="${E2E_BASE_URL:-http://161.118.168.6}"
export E2E_SSH="${E2E_SSH:-oracle-2nd}"

GOFLAGS_EXTRA=()
if [[ "${E2E_VET:-1}" == "1" ]]; then
  go vet ./...
fi

exec go test -v -count=1 -timeout "${E2E_TIMEOUT:-90m}" "${GOFLAGS_EXTRA[@]+"${GOFLAGS_EXTRA[@]}"}" "$@" ./...
