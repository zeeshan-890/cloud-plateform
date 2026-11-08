#!/usr/bin/env bash
# Backward-compatible entry point. Everything now lives in install.sh (installs Docker, opens the firewall,
# generates secrets, starts the stack, verifies health). Usage is identical:
#   curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/scripts/setup-server.sh | bash -s -- --domain apps.example.com --email me@example.com
set -euo pipefail
main() {
  local here=""
  if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"; fi
  if [[ -n "$here" && -f "$here/install.sh" ]]; then exec bash "$here/install.sh" "$@"; fi
  curl -fsSL "https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/${JP_REF:-main}/install.sh" | bash -s -- "$@"
}
main "$@"
