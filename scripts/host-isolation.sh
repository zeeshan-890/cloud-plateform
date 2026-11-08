#!/usr/bin/env bash
# Isolate containers from the HOST itself.
#
# Tenant containers live on private bridge networks (br-*). Without a rule, anything on those bridges can open NEW
# connections to services listening on the host (sshd, rpcbind, ...) via the bridge gateway address. Container to
# container and container to internet traffic is FORWARD traffic and is unaffected; only INPUT (to the host) is dropped.
# Return traffic for connections the host opened (conntrack ESTABLISHED/RELATED) is still allowed.
#
#   scripts/host-isolation.sh apply     install (idempotent) + persist across reboots
#   scripts/host-isolation.sh remove
#   scripts/host-isolation.sh status
set -euo pipefail

SUDO=""; [ "$(id -u)" -eq 0 ] || SUDO="sudo"
CHAIN="JP-HOST-ISOLATION"

have() { command -v "$1" >/dev/null 2>&1; }

rule_present() { $SUDO iptables -C INPUT -j "$CHAIN" 2>/dev/null; }

apply() {
  have iptables || { echo "iptables not found; install it or add an equivalent nftables rule" >&2; exit 1; }
  $SUDO iptables -N "$CHAIN" 2>/dev/null || true
  $SUDO iptables -F "$CHAIN"
  # established traffic and loopback are always fine
  $SUDO iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
  # new connections arriving from any docker bridge (custom networks: br-*, default bridge: docker0) are dropped
  $SUDO iptables -A "$CHAIN" -i br-+ -m conntrack --ctstate NEW -j DROP
  $SUDO iptables -A "$CHAIN" -i docker0 -m conntrack --ctstate NEW -j DROP
  $SUDO iptables -A "$CHAIN" -j RETURN
  rule_present || $SUDO iptables -I INPUT 1 -j "$CHAIN"
  persist
  echo "host isolation active"
}

persist() {
  # Re-create the rule at boot independent of the distro's firewall tooling.
  local unit=/etc/systemd/system/jp-host-isolation.service
  if have systemctl && [ -d /run/systemd/system ]; then
    local self; self="$(readlink -f "$0")"
    local dst=/usr/local/sbin/jp-host-isolation.sh
    $SUDO install -m 0755 "$self" "$dst"
    $SUDO tee "$unit" >/dev/null <<EOF
[Unit]
Description=JP Cloud: block container -> host connections
After=network-online.target docker.service firewalld.service iptables.service
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=$dst apply-no-persist

[Install]
WantedBy=multi-user.target
EOF
    $SUDO systemctl daemon-reload
    $SUDO systemctl enable jp-host-isolation.service >/dev/null 2>&1 || true
  fi
}

remove() {
  $SUDO iptables -D INPUT -j "$CHAIN" 2>/dev/null || true
  $SUDO iptables -F "$CHAIN" 2>/dev/null || true
  $SUDO iptables -X "$CHAIN" 2>/dev/null || true
  if have systemctl; then
    $SUDO systemctl disable --now jp-host-isolation.service >/dev/null 2>&1 || true
    $SUDO rm -f /etc/systemd/system/jp-host-isolation.service /usr/local/sbin/jp-host-isolation.sh
    $SUDO systemctl daemon-reload 2>/dev/null || true
  fi
  echo "host isolation removed"
}

case "${1:-apply}" in
  apply) apply ;;
  apply-no-persist)
    have iptables || exit 0
    $SUDO iptables -N "$CHAIN" 2>/dev/null || true
    $SUDO iptables -F "$CHAIN"
    $SUDO iptables -A "$CHAIN" -m conntrack --ctstate ESTABLISHED,RELATED -j RETURN
    $SUDO iptables -A "$CHAIN" -i br-+ -m conntrack --ctstate NEW -j DROP
    $SUDO iptables -A "$CHAIN" -i docker0 -m conntrack --ctstate NEW -j DROP
    $SUDO iptables -A "$CHAIN" -j RETURN
    rule_present || $SUDO iptables -I INPUT 1 -j "$CHAIN"
    ;;
  remove) remove ;;
  status) $SUDO iptables -S "$CHAIN" 2>/dev/null || echo "not installed" ;;
  *) echo "usage: $0 apply|remove|status" >&2; exit 2 ;;
esac
