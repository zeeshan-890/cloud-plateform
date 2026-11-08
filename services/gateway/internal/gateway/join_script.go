package gateway

// joinScriptTemplate is served to the server that is being added. __BASE__ and __TOKEN__ are replaced per request.
// It must stay free of backticks (Go raw string) and only use tools present on a minimal Linux server.
const joinScriptTemplate = `#!/usr/bin/env bash
# JP Cloud: join this server to the cluster. Safe to run again at any time.
# What it does: reads this server's network facts, opens the planned ports in THIS server's own firewall (private
# ports only for your private network), tests every peer port over the private and the public network, and reports
# the result to your control panel. It installs nothing and runs no background process.
# Skip firewall changes with:  JP_JOIN_NO_FIREWALL=1
set -u
BASE="__BASE__"
TOKEN="__TOKEN__"
API="$BASE/api/v1/cluster/join/$TOKEN"
say() { printf '%s\n' "$*"; }
die() { printf '\n[FAIL] %s\n' "$*" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || die "curl is required on this server."
SUDO=""
if [ "$(id -u)" -ne 0 ]; then
  if command -v sudo >/dev/null 2>&1; then SUDO="sudo"; else say "[WARN] not root and sudo is missing: firewall changes will be skipped."; JP_JOIN_NO_FIREWALL=1; fi
fi

say "JP Cloud: joining this server..."
PLAN=$(curl -fsS --max-time 20 "$API/plan") || die "Cannot reach the control panel at $BASE, or this join command expired (they last 1 hour). Check that this server can make outbound connections to it, then create a new command in the panel."
NAME=$(printf '%s\n' "$PLAN" | sed -n 's/^name=//p' | head -1)
[ -n "$NAME" ] || die "The control panel returned an invalid plan."
say "Server name in the panel: $NAME"

# ---- facts
HOST=$(hostname 2>/dev/null | tr -cd 'A-Za-z0-9.-' | cut -c1-63)
PRIVIP=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<NF;i++) if($i=="src"){print $(i+1); exit}}')
PRIVCIDR=""
if [ -n "$PRIVIP" ]; then PRIVCIDR=$(ip -4 -o addr show 2>/dev/null | awk -v ip="$PRIVIP" '{split($4,a,"/"); if(a[1]==ip){print $4; exit}}'); fi
NET="$PRIVCIDR"
if [ -n "$PRIVCIDR" ] && command -v python3 >/dev/null 2>&1; then NET=$(python3 -c 'import ipaddress,sys;print(ipaddress.ip_interface(sys.argv[1]).network)' "$PRIVCIDR" 2>/dev/null || echo "$PRIVCIDR"); fi
TAG=$(cat /sys/class/dmi/id/chassis_asset_tag /sys/class/dmi/id/sys_vendor /sys/class/dmi/id/product_name 2>/dev/null | tr 'A-Z' 'a-z')
case "$TAG" in
  *oraclecloud*) PROVIDER=oracle ;;
  *amazon*|*ec2*) PROVIDER=aws ;;
  *google*) PROVIDER=gcp ;;
  *microsoft*) PROVIDER=azure ;;
  *) PROVIDER=other ;;
esac
OSNAME=$( (. /etc/os-release 2>/dev/null && printf '%s' "$PRETTY_NAME") | tr -cd 'A-Za-z0-9 ._()-' | cut -c1-60)
DOCKER=false; command -v docker >/dev/null 2>&1 && DOCKER=true
say "Private address: ${PRIVIP:-none}  network: ${NET:-unknown}  provider: $PROVIDER"

# ---- this server's own firewall
FW=none
if command -v firewall-cmd >/dev/null 2>&1 && $SUDO firewall-cmd --state >/dev/null 2>&1; then FW=firewalld
elif command -v ufw >/dev/null 2>&1 && $SUDO ufw status 2>/dev/null | grep -q "Status: active"; then FW=ufw; fi
OPENED=""
if [ "${JP_JOIN_NO_FIREWALL:-0}" != "1" ]; then
  while read -r port scope; do
    [ -n "$port" ] || continue
    ok=1
    case "$FW" in
      firewalld)
        if [ "$scope" = "private" ]; then
          if [ -n "$NET" ]; then $SUDO firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=$NET port port=$port protocol=tcp accept" >/dev/null 2>&1 || ok=0; else ok=0; fi
        else $SUDO firewall-cmd --permanent --add-port="$port/tcp" >/dev/null 2>&1 || ok=0; fi ;;
      ufw)
        if [ "$scope" = "private" ]; then
          if [ -n "$NET" ]; then $SUDO ufw allow from "$NET" to any port "$port" proto tcp >/dev/null 2>&1 || ok=0; else ok=0; fi
        else $SUDO ufw allow "$port/tcp" >/dev/null 2>&1 || ok=0; fi ;;
      *) ok=1 ;;
    esac
    if [ "$ok" = "1" ]; then OPENED="$OPENED${OPENED:+,}$port"; say "Port $port ($scope): allowed in this server's firewall ($FW)"; else say "[WARN] could not open port $port in $FW"; fi
  done <<EOF_OPEN
$(printf '%s\n' "$PLAN" | sed -n 's/^open=//p')
EOF_OPEN
  [ "$FW" = "firewalld" ] && $SUDO firewall-cmd --reload >/dev/null 2>&1
fi

# ---- node agent: only when this server should run projects
IS_NODE=$(printf '%s\n' "$PLAN" | sed -n 's/^node=//p' | head -1)
if [ "$IS_NODE" = "1" ]; then
  ATOKEN=$(printf '%s\n' "$PLAN" | sed -n 's/^agent_token=//p' | head -1)
  APORT=$(printf '%s\n' "$PLAN" | sed -n 's/^agent_port=//p' | head -1)
  AURL=$(printf '%s\n' "$PLAN" | sed -n 's/^agent_url=//p' | head -1)
  ASRC=$(printf '%s\n' "$PLAN" | sed -n 's/^agent_source=//p' | head -1)
  REG=$(printf '%s\n' "$PLAN" | sed -n 's/^registry=//p' | head -1)
  [ -n "$ATOKEN" ] && [ -n "$APORT" ] && [ -n "$AURL" ] || die "The control panel did not send the node agent settings."
  [ -n "$PRIVIP" ] || die "This server has no private address; projects can only run on servers in the same private network as the control plane."
  [ "$(id -u)" -eq 0 ] || [ -n "$SUDO" ] || die "Installing the node agent needs root (or sudo)."
  say ""
  say "Setting this server up to run projects..."
  if ! command -v docker >/dev/null 2>&1; then
    say "Docker is not installed; installing it (this can take several minutes on a small server)..."
    if command -v dnf >/dev/null 2>&1; then
      $SUDO dnf -y install dnf-plugins-core >/dev/null 2>&1
      $SUDO dnf config-manager --add-repo https://download.docker.com/linux/rhel/docker-ce.repo >/dev/null 2>&1
      $SUDO dnf -y install docker-ce docker-ce-cli containerd.io >/dev/null 2>&1 || die "Could not install Docker with dnf. Install Docker yourself, then run this command again."
    elif command -v apt-get >/dev/null 2>&1; then
      curl -fsSL https://get.docker.com | $SUDO sh >/dev/null 2>&1 || die "Could not install Docker. Install Docker yourself, then run this command again."
    else
      die "Docker is not installed and this system's package manager is not supported. Install Docker, then run this command again."
    fi
    $SUDO systemctl enable --now docker >/dev/null 2>&1
    command -v docker >/dev/null 2>&1 || die "Docker did not install correctly."
    DOCKER=true
  fi
  $SUDO systemctl enable --now docker >/dev/null 2>&1
  case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; *) die "Unsupported CPU architecture $(uname -m) (amd64 and arm64 are supported)." ;; esac
  TMPB=$(mktemp)
  curl -fsS --max-time 180 "$AURL?arch=$ARCH" -o "$TMPB" || die "Could not download the node agent from the control panel (the command may have expired)."
  $SUDO install -m 0755 "$TMPB" /usr/local/bin/jp-node-agent; rm -f "$TMPB"
  ENVF=$(mktemp)
  { printf 'NODE_TOKEN=%s\n' "$ATOKEN"; printf 'NODE_LISTEN=%s:%s\n' "$PRIVIP" "$APORT"; printf 'NODE_NAME=%s\n' "$NAME"; } > "$ENVF"
  [ -n "$REG" ] && printf 'NODE_REGISTRY_UPSTREAM=%s\n' "$REG" >> "$ENVF"
  $SUDO install -m 0600 "$ENVF" /etc/jp-node-agent.env; rm -f "$ENVF"
  UNIT=$(mktemp)
  cat > "$UNIT" <<'EOF_UNIT'
[Unit]
Description=JP Cloud node agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
EnvironmentFile=/etc/jp-node-agent.env
ExecStart=/usr/local/bin/jp-node-agent
Restart=always
RestartSec=3
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
EOF_UNIT
  $SUDO install -m 0644 "$UNIT" /etc/systemd/system/jp-node-agent.service; rm -f "$UNIT"
  $SUDO systemctl daemon-reload
  $SUDO systemctl enable jp-node-agent >/dev/null 2>&1
  $SUDO systemctl restart jp-node-agent
  # let the control plane reach the agent and the published app ports (only from its address / this private network)
  SRC="${ASRC:-$NET}"
  if [ "${JP_JOIN_NO_FIREWALL:-0}" != "1" ] && [ -n "$SRC" ]; then
    case "$FW" in
      firewalld)
        $SUDO firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=$SRC port port=$APORT protocol=tcp accept" >/dev/null 2>&1
        $SUDO firewall-cmd --permanent --add-rich-rule="rule family=ipv4 source address=$SRC port port=32768-60999 protocol=tcp accept" >/dev/null 2>&1
        $SUDO firewall-cmd --reload >/dev/null 2>&1 ;;
      ufw)
        $SUDO ufw allow from "$SRC" to any port "$APORT" proto tcp >/dev/null 2>&1
        $SUDO ufw allow from "$SRC" to any port 32768:60999 proto tcp >/dev/null 2>&1 ;;
    esac
    say "Allowed TCP $APORT and 32768-60999 from $SRC in this server's firewall"
  fi
  ok=0
  for i in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS --max-time 3 "http://$PRIVIP:$APORT/node/health" >/dev/null 2>&1; then ok=1; break; fi
    sleep 1
  done
  if [ "$ok" = "1" ]; then say "Node agent is running on $PRIVIP:$APORT"; else say "[WARN] the node agent did not answer yet; check: sudo journalctl -u jp-node-agent -n 30"; fi
fi

# ---- connectivity tests (this server -> every peer port)
CHECKS=""
# Starting a subprocess costs a few ms on small servers; measure it once and subtract it from every connect time.
BASE_US=0
for i in 1 2 3; do s=$(date +%s%N); timeout 4 bash -c ':' ; e=$(date +%s%N); BASE_US=$((BASE_US+(e-s)/3000)); done
while read -r peer path addr port; do
  [ -n "$peer" ] || continue
  ok=false; n=0; tot=0; err=""
  for i in 1 2 3; do
    s=$(date +%s%N)
    out=$(timeout 4 bash -c "exec 3<>/dev/tcp/$addr/$port" 2>&1); rc=$?
    if [ $rc -eq 0 ]; then e=$(date +%s%N); ok=true; n=$((n+1)); us=$(((e-s)/1000-BASE_US)); [ $us -lt 0 ] && us=0; tot=$((tot+us))
    else
      case "$out" in *refused*) err=refused ;; *"No route"*) err=no_route ;; *) [ $rc -eq 124 ] && err=timeout || err=unreachable ;; esac
    fi
  done
  ms=0; [ $n -gt 0 ] && ms=$(awk -v t="$tot" -v n="$n" 'BEGIN{printf "%.1f", t/n/1000}')
  [ "$ok" = true ] && err=""
  say "  $peer $path $addr:$port -> $([ "$ok" = true ] && echo "open ${ms} ms" || echo "$err")"
  CHECKS="$CHECKS${CHECKS:+,}{\"peer\":\"$peer\",\"path\":\"$path\",\"addr\":\"$addr\",\"port\":$port,\"ok\":$ok,\"ms\":$ms,\"error\":\"$err\"}"
done <<EOF_TESTS
$(printf '%s\n' "$PLAN" | sed -n 's/^test=//p')
EOF_TESTS

LISTEN=$(ss -ltnH 2>/dev/null | awk '{n=split($4,a,":"); print a[n]}' | grep -E '^[0-9]+$' | sort -un | tr '\n' ',' | sed 's/,$//')

# ---- report
BODY=$(printf '{"hostname":"%s","private_ip":"%s","private_cidr":"%s","provider":"%s","os":"%s","firewall":"%s","docker":%s,"opened_ports":[%s],"listening_ports":[%s],"checks":[%s]}' \
  "$HOST" "$PRIVIP" "$PRIVCIDR" "$PROVIDER" "$OSNAME" "$FW" "$DOCKER" "$OPENED" "$LISTEN" "$CHECKS")
if ! printf '%s' "$BODY" | curl -fsS --max-time 90 -X POST -H 'Content-Type: application/json' --data-binary @- "$API/report?format=text"; then
  die "Could not send the report to the control panel. The join command may have expired; create a new one."
fi
`
