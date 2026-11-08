#!/usr/bin/env bash
# ==============================================================================
# JP Cloud - single-server installer (Linux: Oracle Linux/RHEL/Rocky/Alma/Fedora, Debian, Ubuntu; x86_64 + arm64)
#
#   curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/install.sh | bash -s -- [options]
#   ./install.sh [options]                       (from a checkout)
#
# Options
#   --domain DOMAIN        ROOT_DOMAIN: apps are served at <project>-<org>.DOMAIN. Needs wildcard DNS  *.DOMAIN -> this server.
#   --panel-domain HOST    hostname of the management panel (default: panel.DOMAIN)
#   --email EMAIL          Let's Encrypt contact (required for TLS)
#   --no-tls               serve plain http even with a domain
#   --ip-only              no domain: panel on http://<ip> and http://<ip>:3000, apps under <ip-dashes>.sslip.io (opens port 3000)
#   --public-ip IP         override public IP auto-detection
#   --registration MODE    closed | invite (default) | open
#   --tier TIER            auto (default) | small | medium | large | xlarge
#   --monitoring           also start prometheus/grafana/loki/tempo/promtail
#   --dir PATH             checkout location when run via curl|bash (default: $HOME/cloud-plateform)
#   --ref REF              git branch/tag to install (default: main)
#   --repo URL             git repository to clone (default: https://github.com/zeeshan-890/cloud-plateform.git)
#   --skip-docker          do not install Docker (fail if missing)
#   --skip-firewall        do not touch the host firewall
#   --no-update            do not git pull an existing checkout
#   --swap SIZE            create a swapfile (e.g. 2G) when the host has none
#   -y, --yes              never prompt (defaults to --ip-only when no --domain is given)
#
# Idempotent: re-running keeps the existing .env (only missing keys are added), pulls updates and re-applies compose.
# Every statement lives inside a function and `main` runs on the LAST line, so a truncated download cannot half-run.
# ==============================================================================
set -Eeuo pipefail

JP_REPO_DEFAULT="https://github.com/zeeshan-890/cloud-plateform.git"

# ----------------------------------------------------------------------------- helpers
c_off=$'\033[0m'; c_red=$'\033[31m'; c_grn=$'\033[32m'; c_ylw=$'\033[33m'; c_cyn=$'\033[36m'
log()  { printf '%s==>%s %s\n' "$c_cyn" "$c_off" "$*"; }
ok()   { printf '%s ok%s %s\n' "$c_grn" "$c_off" "$*"; }
warn() { printf '%swarn%s %s\n' "$c_ylw" "$c_off" "$*" >&2; }
die()  { printf '%serror%s %s\n' "$c_red" "$c_off" "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

usage() { sed -n '2,/^# Idempotent/p' "${BASH_SOURCE[0]:-$0}" 2>/dev/null | sed 's/^# \{0,1\}//' | head -n -1; }

# ----------------------------------------------------------------------------- state
DOMAIN="" PANEL_DOMAIN="" EMAIL="" TLS="auto" IP_ONLY=false PUBLIC_IP="" REGISTRATION="invite"
TIER="auto" MONITORING=false INSTALL_DIR="" REF="main" REPO="$JP_REPO_DEFAULT" SKIP_DOCKER=false
SKIP_FIREWALL=false NO_UPDATE=false SWAP="" ASSUME_YES=false
SUDO="" JP_DOCKER="docker" ROOT_DIR="" ARCH="" OS_ID="" OS_LIKE="" OS_FAMILY="" OS_VERSION=""
ENV_CREATED=false
EXPLICIT_MODE=false

parse_args() {
  while (($#)); do
    case "$1" in
      --domain) DOMAIN="${2:?--domain needs a value}"; EXPLICIT_MODE=true; shift ;;
      --panel-domain) PANEL_DOMAIN="${2:?--panel-domain needs a value}"; shift ;;
      --email) EMAIL="${2:?--email needs a value}"; shift ;;
      --no-tls) TLS=false ;;
      --ip-only) IP_ONLY=true; EXPLICIT_MODE=true ;;
      --public-ip) PUBLIC_IP="${2:?--public-ip needs a value}"; shift ;;
      --registration) REGISTRATION="${2:?--registration needs a value}"; shift ;;
      --tier) TIER="${2:?--tier needs a value}"; shift ;;
      --monitoring) MONITORING=true ;;
      --dir) INSTALL_DIR="${2:?--dir needs a value}"; shift ;;
      --ref) REF="${2:?--ref needs a value}"; shift ;;
      --repo) REPO="${2:?--repo needs a value}"; shift ;;
      --skip-docker) SKIP_DOCKER=true ;;
      --skip-firewall) SKIP_FIREWALL=true ;;
      --no-update) NO_UPDATE=true ;;
      --swap) SWAP="${2:?--swap needs a value}"; shift ;;
      -y|--yes) ASSUME_YES=true ;;
      -h|--help) usage; exit 0 ;;
      *) die "unknown option: $1 (try --help)" ;;
    esac
    shift
  done
  case "$REGISTRATION" in closed|invite|open) ;; *) die "--registration must be closed, invite or open" ;; esac
  if [[ "$IP_ONLY" == true && -n "$DOMAIN" ]]; then die "--ip-only and --domain are mutually exclusive"; fi
}

# prompt VAR "question" default  (reads from the terminal even under curl|bash)
ask() {
  local __var="$1" q="$2" def="${3:-}" ans=""
  if [[ "$ASSUME_YES" == true || ! -r /dev/tty ]]; then printf -v "$__var" '%s' "$def"; return 0; fi
  read -r -p "$q${def:+ [$def]}: " ans </dev/tty || true
  printf -v "$__var" '%s' "${ans:-$def}"
}

rand_hex() { # rand_hex <bytes>
  if have openssl; then openssl rand -hex "$1"; else head -c "$1" /dev/urandom | od -An -tx1 | tr -d ' \n'; fi
}
rand_b64() { # base64 of <bytes> random bytes
  if have openssl; then openssl rand -base64 "$1" | tr -d '\n'; else head -c "$1" /dev/urandom | base64 | tr -d '\n'; fi
}

valid_hostname() { [[ "$1" =~ ^([A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?\.)+[A-Za-z]{2,63}$ ]]; }
valid_ipv4() { [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && awk -F. -v ip="$1" 'BEGIN{n=split(ip,a,"."); for(i=1;i<=n;i++) if (a[i]>255) exit 1; exit 0}'; }

# ----------------------------------------------------------------------------- preflight
preflight() {
  [[ "$(uname -s)" == "Linux" ]] || die "install.sh supports Linux only (use install.ps1 on Windows for local development)"
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported CPU architecture: $(uname -m) (need x86_64 or aarch64)" ;;
  esac
  if [[ "$(id -u)" -ne 0 ]]; then
    have sudo || die "run as root or install sudo"
    SUDO="sudo"
    sudo -v || die "sudo access is required"
  fi
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    OS_ID="${ID:-unknown}"; OS_LIKE="${ID_LIKE:-}"; OS_VERSION="${VERSION_ID:-}"
  fi
  case " $OS_ID $OS_LIKE " in
    *" debian "*|*" ubuntu "*) OS_FAMILY=debian ;;
    *" rhel "*|*" fedora "*|*" centos "*|*" ol "*) OS_FAMILY=rhel ;;
    *) OS_FAMILY=other ;;
  esac
  log "host: ${OS_ID} ${OS_VERSION} (${OS_FAMILY} family), ${ARCH}"

  local ram_kb free_kb
  ram_kb="$(awk '/MemTotal/ {print $2}' /proc/meminfo)"
  if ((ram_kb < 3500000)); then warn "less than 4 GB RAM ($((ram_kb / 1024)) MB): builds may be OOM-killed. Consider --swap 2G."; fi
  free_kb="$(df -Pk "${HOME:-/}" | awk 'NR==2 {print $4}')"
  if ((free_kb < 10485760)); then warn "less than 10 GB free disk space; image builds need several GB."; fi
}

pkg_install() { # distro-agnostic minimal package install
  case "$OS_FAMILY" in
    debian) $SUDO apt-get update -y >/dev/null && DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y "$@" ;;
    rhel)   $SUDO dnf install -y "$@" ;;
    *)      die "cannot install packages ($*) on '$OS_ID'; install them manually and re-run" ;;
  esac
}

install_base_tools() {
  local missing=()
  for t in git curl openssl; do have "$t" || missing+=("$t"); done
  if ((${#missing[@]})); then
    log "installing ${missing[*]}"
    pkg_install "${missing[@]}" ca-certificates
  fi
}

setup_swap() {
  [[ -n "$SWAP" ]] || return 0
  if [[ -n "$(swapon --show --noheadings 2>/dev/null)" ]]; then ok "swap already present"; return 0; fi
  log "creating $SWAP swapfile at /swapfile"
  $SUDO fallocate -l "$SWAP" /swapfile 2>/dev/null || $SUDO dd if=/dev/zero of=/swapfile bs=1M count="$(( ${SWAP%G} * 1024 ))" status=none
  $SUDO chmod 600 /swapfile && $SUDO mkswap /swapfile >/dev/null && $SUDO swapon /swapfile
  grep -q '^/swapfile ' /etc/fstab || echo '/swapfile none swap sw 0 0' | $SUDO tee -a /etc/fstab >/dev/null
}

# ----------------------------------------------------------------------------- docker
docker_ok() { have docker && $SUDO docker compose version >/dev/null 2>&1; }

install_docker_debian() {
  local distro codename
  case " $OS_ID $OS_LIKE " in *" ubuntu "*) distro=ubuntu ;; *) distro=debian ;; esac
  # shellcheck disable=SC1091
  codename="$( . /etc/os-release; echo "${UBUNTU_CODENAME:-${VERSION_CODENAME:-}}")"
  [[ -n "$codename" ]] || die "cannot determine distro codename for the Docker apt repository"
  for p in docker.io docker-doc docker-compose podman-docker containerd runc; do
    $SUDO apt-get remove -y "$p" >/dev/null 2>&1 || true
  done
  $SUDO install -m 0755 -d /etc/apt/keyrings
  curl -fsSL "https://download.docker.com/linux/${distro}/gpg" | $SUDO gpg --dearmor --yes -o /etc/apt/keyrings/docker.gpg
  $SUDO chmod a+r /etc/apt/keyrings/docker.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/${distro} ${codename} stable" |
    $SUDO tee /etc/apt/sources.list.d/docker.list >/dev/null
  $SUDO apt-get update -y >/dev/null
  DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

install_docker_rhel() {
  local repo="centos"
  [[ "$OS_ID" == "fedora" ]] && repo="fedora"
  # Oracle Linux / RHEL ship podman-docker (provides /usr/bin/docker) and runc, which conflict with docker-ce.
  $SUDO dnf remove -y podman-docker docker docker-client docker-client-latest docker-common docker-latest \
    docker-latest-logrotate docker-logrotate docker-engine >/dev/null 2>&1 || true
  curl -fsSL "https://download.docker.com/linux/${repo}/docker-ce.repo" | $SUDO tee /etc/yum.repos.d/docker-ce.repo >/dev/null
  # --allowerasing lets dnf replace conflicting runc/podman packages with containerd.io
  $SUDO dnf install -y --allowerasing docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
}

install_docker_fallback() {
  warn "falling back to the get.docker.com convenience script"
  curl -fsSL https://get.docker.com | $SUDO sh
}

ensure_docker() {
  if [[ "$SKIP_DOCKER" == true ]]; then
    have docker || die "Docker is not installed (--skip-docker given)"
  elif ! docker_ok; then
    log "installing Docker Engine + compose + buildx"
    case "$OS_FAMILY" in
      debian) install_docker_debian || install_docker_fallback ;;
      rhel)   install_docker_rhel || install_docker_fallback ;;
      *)      install_docker_fallback ;;
    esac
  fi

  # make sure the plugins exist even if docker was pre-installed
  if ! $SUDO docker compose version >/dev/null 2>&1; then
    case "$OS_FAMILY" in
      debian) DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y docker-compose-plugin ;;
      rhel)   $SUDO dnf install -y docker-compose-plugin ;;
    esac
  fi
  if ! $SUDO docker buildx version >/dev/null 2>&1; then
    case "$OS_FAMILY" in
      debian) DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y docker-buildx-plugin || true ;;
      rhel)   $SUDO dnf install -y docker-buildx-plugin || true ;;
    esac
  fi

  if have systemctl; then $SUDO systemctl enable --now docker >/dev/null 2>&1 || true; fi
  for _ in $(seq 1 20); do
    if $SUDO docker info >/dev/null 2>&1; then break; fi
    sleep 2
  done
  $SUDO docker info >/dev/null 2>&1 || die "Docker daemon is not running (check: systemctl status docker)"
  $SUDO docker compose version >/dev/null 2>&1 || die "Docker Compose v2 plugin is missing"

  if [[ -n "$SUDO" ]] && ! id -nG "$USER" | tr ' ' '\n' | grep -qx docker; then
    $SUDO usermod -aG docker "$USER" 2>/dev/null || true
    warn "added $USER to the docker group (takes effect on next login); this run uses sudo"
  fi
  if docker info >/dev/null 2>&1; then JP_DOCKER="docker"; else JP_DOCKER="sudo docker"; fi
  ok "docker $($SUDO docker version --format '{{.Server.Version}}' 2>/dev/null || echo '?'), compose $($SUDO docker compose version --short 2>/dev/null || echo '?')"
}

tune_kernel() {
  # Redis wants overcommit; harmless elsewhere
  echo 'vm.overcommit_memory = 1' | $SUDO tee /etc/sysctl.d/99-jp.conf >/dev/null 2>&1 || true
  $SUDO sysctl -q -w vm.overcommit_memory=1 >/dev/null 2>&1 || true
}

# ----------------------------------------------------------------------------- firewall
iptables_open() { # iptables_open <iptables-binary> <port>
  local ipt="$1" port="$2" pos policy
  $SUDO "$ipt" -C INPUT -p tcp --dport "$port" -j ACCEPT 2>/dev/null && return 0
  # Oracle Cloud images ship "-A INPUT -j REJECT --reject-with icmp-host-prohibited"; ACCEPT must precede it.
  pos="$($SUDO "$ipt" -L INPUT --line-numbers -n 2>/dev/null | awk '$2=="REJECT"||$2=="DROP"{print $1; exit}')"
  if [[ -n "$pos" ]]; then
    $SUDO "$ipt" -I INPUT "$pos" -p tcp --dport "$port" -j ACCEPT
    IPT_CHANGED=true
  else
    policy="$($SUDO "$ipt" -S INPUT 2>/dev/null | awk '/^-P INPUT/ {print $3}')"
    if [[ "$policy" == "DROP" || "$policy" == "REJECT" ]]; then
      $SUDO "$ipt" -A INPUT -p tcp --dport "$port" -j ACCEPT
      IPT_CHANGED=true
    fi
  fi
}

persist_iptables() {
  if have netfilter-persistent; then
    $SUDO netfilter-persistent save >/dev/null
  elif [[ -d /etc/iptables ]]; then
    $SUDO iptables-save | $SUDO tee /etc/iptables/rules.v4 >/dev/null
    if have ip6tables-save; then $SUDO ip6tables-save | $SUDO tee /etc/iptables/rules.v6 >/dev/null; fi
  elif [[ -f /etc/sysconfig/iptables ]]; then
    $SUDO iptables-save | $SUDO tee /etc/sysconfig/iptables >/dev/null
  elif [[ "$OS_FAMILY" == debian ]]; then
    echo iptables-persistent iptables-persistent/autosave_v4 boolean true | $SUDO debconf-set-selections 2>/dev/null || true
    echo iptables-persistent iptables-persistent/autosave_v6 boolean true | $SUDO debconf-set-selections 2>/dev/null || true
    if DEBIAN_FRONTEND=noninteractive $SUDO apt-get install -y iptables-persistent >/dev/null 2>&1; then
      $SUDO netfilter-persistent save >/dev/null 2>&1 || warn "could not persist iptables rules; they will be lost on reboot"
    else
      warn "could not install iptables-persistent; rules will be lost on reboot"
    fi
  else
    warn "could not persist iptables rules (no netfilter-persistent, /etc/iptables or /etc/sysconfig/iptables); they will be lost on reboot"
  fi
}

# Block NEW connections from container bridges to the host itself (sshd, rpcbind, ...). See scripts/host-isolation.sh.
isolate_host() {
  if [[ "$SKIP_FIREWALL" == true ]]; then warn "skipping host isolation (--skip-firewall)"; return 0; fi
  if [[ -x "$ROOT_DIR/scripts/host-isolation.sh" || -f "$ROOT_DIR/scripts/host-isolation.sh" ]]; then
    bash "$ROOT_DIR/scripts/host-isolation.sh" apply >/dev/null && ok "host isolation: containers cannot open connections to the host" || warn "host isolation not applied (iptables missing?); containers can reach host services"
  fi
}

configure_firewall() {
  if [[ "$SKIP_FIREWALL" == true ]]; then warn "skipping firewall (--skip-firewall)"; return 0; fi
  local ports=(80 443) p
  if [[ "$IP_ONLY" == true ]]; then ports+=(3000); fi
  log "firewall: opening ${ports[*]} (port 22 is never touched)"
  IPT_CHANGED=false
  if have firewall-cmd && $SUDO firewall-cmd --state >/dev/null 2>&1; then
    for p in "${ports[@]}"; do $SUDO firewall-cmd --permanent --add-port="${p}/tcp" >/dev/null; done
    $SUDO firewall-cmd --reload >/dev/null
    ok "firewalld: opened ${ports[*]}"
  elif have ufw && $SUDO ufw status 2>/dev/null | grep -q '^Status: active'; then
    for p in "${ports[@]}"; do $SUDO ufw allow "${p}/tcp" >/dev/null; done
    ok "ufw: opened ${ports[*]}"
  elif have iptables; then
    for p in "${ports[@]}"; do
      iptables_open iptables "$p"
      if have ip6tables; then iptables_open ip6tables "$p" || true; fi
    done
    if [[ "$IPT_CHANGED" == true ]]; then persist_iptables; ok "iptables: inserted ACCEPT rules before the REJECT rule and saved them"; else ok "iptables: nothing to change"; fi
  else
    warn "no host firewall found; make sure ${ports[*]} are reachable"
  fi
  warn "cloud firewalls (Oracle VCN security list / AWS security group) are NOT managed here: allow ${ports[*]} inbound there too"
}

# ----------------------------------------------------------------------------- repository
locate_repo() {
  local here=""
  if [[ -n "${BASH_SOURCE[0]:-}" && -f "${BASH_SOURCE[0]}" ]]; then here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"; fi
  if [[ -n "$here" && -f "$here/infra/compose/docker-compose.yml" ]]; then
    ROOT_DIR="$here"
    if [[ "$NO_UPDATE" != true && -d "$ROOT_DIR/.git" ]]; then
      if git -C "$ROOT_DIR" pull --ff-only >/dev/null 2>&1; then ok "repository up to date"; else warn "git pull skipped (local changes or no network)"; fi
    fi
    return 0
  fi
  INSTALL_DIR="${INSTALL_DIR:-$HOME/cloud-plateform}"
  if [[ -d "$INSTALL_DIR/.git" ]]; then
    log "updating $INSTALL_DIR"
    if [[ "$NO_UPDATE" != true ]]; then
      if ! { git -C "$INSTALL_DIR" fetch --quiet origin "$REF" && git -C "$INSTALL_DIR" checkout --quiet "$REF" &&
             git -C "$INSTALL_DIR" pull --ff-only --quiet origin "$REF"; }; then
        warn "could not update $INSTALL_DIR (local changes?) - continuing with what is there"
      fi
    fi
  else
    log "cloning $REPO ($REF) into $INSTALL_DIR"
    git clone --quiet --branch "$REF" "$REPO" "$INSTALL_DIR"
  fi
  ROOT_DIR="$INSTALL_DIR"
}

# ----------------------------------------------------------------------------- configuration (.env)
detect_public_ip() {
  local ip="" url
  if [[ -n "$PUBLIC_IP" ]]; then valid_ipv4 "$PUBLIC_IP" || die "invalid --public-ip: $PUBLIC_IP"; return 0; fi
  for url in https://api.ipify.org https://ifconfig.me/ip https://icanhazip.com https://ipv4.icanhazip.com; do
    ip="$(curl -4 -fsS --max-time 5 "$url" 2>/dev/null | tr -d '[:space:]' || true)"
    if valid_ipv4 "$ip"; then PUBLIC_IP="$ip"; return 0; fi
  done
  ip="$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}' | head -n1 || true)"
  if valid_ipv4 "$ip"; then
    PUBLIC_IP="$ip"
    warn "could not reach an IP echo service; using local address $ip (may be private)"
    return 0
  fi
  die "could not detect the public IP; pass --public-ip"
}

decide_mode() {
  detect_public_ip
  if [[ -z "$DOMAIN" && "$IP_ONLY" != true ]]; then
    local have_domain=""
    ask have_domain "Do you have a domain pointing (wildcard *.domain) at ${PUBLIC_IP}? (y/N)" "N"
    if [[ "$have_domain" =~ ^[Yy] ]]; then
      ask DOMAIN "Root domain for apps (e.g. apps.example.com)" ""
    else
      IP_ONLY=true
    fi
  fi
  if [[ -n "$DOMAIN" ]]; then
    DOMAIN="${DOMAIN,,}"
    valid_hostname "$DOMAIN" || die "invalid domain: $DOMAIN"
    PANEL_DOMAIN="${PANEL_DOMAIN:-panel.$DOMAIN}"
    valid_hostname "$PANEL_DOMAIN" || die "invalid panel domain: $PANEL_DOMAIN"
    if [[ "$TLS" == auto ]]; then TLS=true; fi
    if [[ "$TLS" == true ]]; then
      if [[ -z "$EMAIL" ]]; then ask EMAIL "Let's Encrypt contact e-mail" ""; fi
      [[ "$EMAIL" =~ ^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$ ]] || die "a valid --email is required for TLS (or use --no-tls)"
    fi
  else
    IP_ONLY=true
    TLS=false
  fi
  if [[ "$TLS" == auto ]]; then TLS=false; fi
}

ensure_key() { # ensure_key KEY VALUE [secret]   append when absent; fill when a *secret* is present but empty
  local key="$1" value="$2" secret="${3:-}" tmp
  if grep -qE "^${key}=" "$ENV_FILE"; then
    if [[ -n "$secret" ]] && grep -qE "^${key}=$" "$ENV_FILE"; then
      tmp="$(mktemp)"
      awk -v k="$key" -v v="$value" 'BEGIN{FS=OFS="="} $1==k && $2=="" {print k "=" v; next} {print}' "$ENV_FILE" >"$tmp"
      cat "$tmp" >"$ENV_FILE"; rm -f "$tmp"
      ADDED+=("$key")
    fi
  else
    printf '%s=%s\n' "$key" "$value" >>"$ENV_FILE"
    ADDED+=("$key")
  fi
}

write_env() {
  ENV_FILE="$ROOT_DIR/.env"
  ADDED=()
  umask 077
  if [[ -f "$ENV_FILE" ]]; then
    log "existing .env found - keeping it (only missing keys are added)"
    if [[ "$EXPLICIT_MODE" == true ]]; then
      warn "domain/TLS flags do not modify an existing .env; edit $ENV_FILE if you want to change them"
    fi
  else
    ENV_CREATED=true
    {
      echo "# JP Cloud - generated by install.sh on $(date -u +%Y-%m-%dT%H:%M:%SZ). Contains secrets: keep chmod 600, never commit."
      echo "# Reference for every variable: .env.example"
    } >"$ENV_FILE"
  fi
  chmod 600 "$ENV_FILE"

  local root_domain panel tls public_url cors dash_bind ip_dashes sock_gid dash_pw dash_hash
  if [[ "$IP_ONLY" == true ]]; then
    ip_dashes="${PUBLIC_IP//./-}"
    root_domain="${ip_dashes}.sslip.io"; panel=""; tls=false
    public_url="http://${PUBLIC_IP}"
    cors="http://${PUBLIC_IP},http://${PUBLIC_IP}:3000"
    dash_bind="0.0.0.0"
  else
    root_domain="$DOMAIN"; panel="$PANEL_DOMAIN"; tls="$TLS"
    if [[ "$tls" == true ]]; then public_url="https://${panel}"; else public_url="http://${panel}"; fi
    cors="$public_url"
    dash_bind="127.0.0.1"
  fi
  sock_gid="$(stat -c %g /var/run/docker.sock 2>/dev/null || echo 0)"
  if ! [[ "$sock_gid" =~ ^[0-9]+$ ]]; then sock_gid=0; fi

  # identity
  ensure_key ROOT_DOMAIN "$root_domain"
  ensure_key MANAGEMENT_DOMAIN "$panel"
  ensure_key PUBLIC_BASE_URL "$public_url"
  ensure_key CORS_ORIGINS "$cors"
  ensure_key PUBLIC_IP "$PUBLIC_IP"
  ensure_key DOMAIN_CNAME_TARGET "cname.${root_domain}"
  ensure_key PREVIEW_BASE_DOMAIN "preview.${root_domain}"
  ensure_key TLS_ENABLED "$tls"
  ensure_key ACME_EMAIL "$EMAIL"
  ensure_key HSTS_SECONDS "0"
  ensure_key JP_ENV production
  ensure_key JP_LOG_LEVEL info
  ensure_key REGISTRATION_MODE "$REGISTRATION"
  ensure_key BUILD_MODE docker
  ensure_key RUNTIME_MODE docker
  ensure_key TRIVY_ENABLED true
  ensure_key DASHBOARD_BIND "$dash_bind"
  ensure_key DOCKER_GID "$sock_gid" secret
  # secrets
  ensure_key JWT_SECRET "$(rand_hex 24)" secret
  ensure_key INTERNAL_TOKEN "$(rand_hex 32)" secret
  ensure_key SECRETS_MASTER_KEY "$(rand_b64 32)" secret
  ensure_key AI_ENC_KEY "$(rand_b64 32)" secret
  ensure_key IDENTITY_ENC_KEY "$(rand_b64 32)" secret
  ensure_key REPO_ENC_KEY "$(rand_b64 32)" secret
  ensure_key POSTGRES_USER jp
  ensure_key POSTGRES_DB jp
  ensure_key POSTGRES_PASSWORD "$(rand_hex 24)" secret
  ensure_key REDIS_PASSWORD "$(rand_hex 24)" secret
  ensure_key REGISTRY_HTTP_SECRET "$(rand_hex 16)" secret
  ensure_key MINIO_ROOT_USER "jp$(rand_hex 5)" secret
  ensure_key MINIO_ROOT_PASSWORD "$(rand_hex 24)" secret
  ensure_key GITHUB_WEBHOOK_SECRET "$(rand_hex 24)" secret
  ensure_key GRAFANA_ADMIN_PASSWORD "$(rand_hex 12)" secret
  if ! grep -qE '^TRAEFIK_DASHBOARD_AUTH=.+' "$ENV_FILE"; then
    dash_pw="$(rand_hex 12)"
    dash_hash="$(openssl passwd -apr1 "$dash_pw")"
    # single quotes: the hash contains $ which docker compose would otherwise interpolate
    sed -i '/^TRAEFIK_DASHBOARD_AUTH=/d;/^TRAEFIK_DASHBOARD_PASSWORD=/d' "$ENV_FILE"
    printf "TRAEFIK_DASHBOARD_AUTH='admin:%s'\n" "$dash_hash" >>"$ENV_FILE"
    printf 'TRAEFIK_DASHBOARD_PASSWORD=%s\n' "$dash_pw" >>"$ENV_FILE"
    ADDED+=(TRAEFIK_DASHBOARD_AUTH)
  fi
  if [[ "$MONITORING" == true ]] && ! grep -qE '^COMPOSE_PROFILES=.*monitoring' "$ENV_FILE"; then
    sed -i '/^COMPOSE_PROFILES=/d' "$ENV_FILE"
    echo 'COMPOSE_PROFILES=monitoring' >>"$ENV_FILE"
  fi
  # optional integrations - present but empty so the file documents them
  for k in GITHUB_APP_ID GITHUB_APP_SLUG GITHUB_APP_PRIVATE_KEY_PATH GITHUB_TOKEN SMTP_HOST SMTP_USERNAME SMTP_PASSWORD SMTP_FROM \
          ; do
    ensure_key "$k" ""
  done
  chmod 600 "$ENV_FILE"

  if [[ "$ENV_CREATED" == true ]]; then ok ".env created (chmod 600) with $(( ${#ADDED[@]} )) generated/defaulted keys"
  elif ((${#ADDED[@]})); then ok "added missing keys to .env: ${ADDED[*]}"
  else ok ".env already complete"; fi

  # validate what compose will insist on, before spending minutes building
  local k
  for k in ROOT_DOMAIN PUBLIC_BASE_URL JWT_SECRET INTERNAL_TOKEN SECRETS_MASTER_KEY AI_ENC_KEY IDENTITY_ENC_KEY REPO_ENC_KEY POSTGRES_PASSWORD REDIS_PASSWORD \
           REGISTRY_HTTP_SECRET MINIO_ROOT_USER MINIO_ROOT_PASSWORD TRAEFIK_DASHBOARD_AUTH; do
    grep -qE "^${k}=.+" "$ENV_FILE" || die ".env is missing a value for ${k}"
  done
  if grep -qE '^TLS_ENABLED=true' "$ENV_FILE"; then
    grep -qE '^ACME_EMAIL=.+' "$ENV_FILE" || die "TLS_ENABLED=true needs ACME_EMAIL in .env"
    grep -qE '^MANAGEMENT_DOMAIN=.+' "$ENV_FILE" || die "TLS_ENABLED=true needs MANAGEMENT_DOMAIN in .env"
  fi
}

check_dns() {
  [[ "$IP_ONLY" == true ]] && return 0
  local panel resolved
  panel="$(grep -E '^MANAGEMENT_DOMAIN=' "$ENV_FILE" | cut -d= -f2-)"
  [[ -n "$panel" ]] || return 0
  resolved="$(getent ahostsv4 "$panel" 2>/dev/null | awk '{print $1; exit}' || true)"
  if [[ -z "$resolved" ]]; then
    warn "DNS: ${panel} does not resolve yet. Create these records at your DNS provider:"
  elif [[ "$resolved" != "$PUBLIC_IP" ]]; then
    warn "DNS: ${panel} resolves to ${resolved}, not ${PUBLIC_IP}. Fix these records:"
  else
    ok "DNS: ${panel} -> ${resolved}"
    return 0
  fi
  local rd
  rd="$(grep -E '^ROOT_DOMAIN=' "$ENV_FILE" | cut -d= -f2-)"
  printf '      A   %s        -> %s\n      A   *.%s      -> %s\n' "$panel" "$PUBLIC_IP" "$rd" "$PUBLIC_IP"
  warn "Let's Encrypt certificates are only issued once DNS resolves to this server (HTTP-01 on port 80)."
}

# ----------------------------------------------------------------------------- start + verify
start_stack() {
  cd "$ROOT_DIR"
  export JP_DOCKER
  export COMPOSE_PARALLEL_LIMIT="${COMPOSE_PARALLEL_LIMIT:-4}"
  log "sizing host"
  bash ./scripts/size-host.sh "$TIER"
  log "building and starting the stack (first build takes several minutes)"
  bash ./scripts/jp-up.sh --pull --size "$TIER"
}

verify_stack() {
  cd "$ROOT_DIR"
  local gw_port dash_port ok_gw=false ok_ui=false ok_edge=false base
  gw_port="$(grep -E '^GATEWAY_PORT=' .env | cut -d= -f2- || true)"; gw_port="${gw_port:-8000}"
  dash_port="$(grep -E '^DASHBOARD_PORT=' .env | cut -d= -f2- || true)"; dash_port="${dash_port:-3000}"
  log "waiting for the gateway and dashboard (up to 5 minutes)"
  for _ in $(seq 1 100); do
    if [[ "$ok_gw" == false ]] && curl -fsS --max-time 3 "http://127.0.0.1:${gw_port}/healthz" >/dev/null 2>&1; then ok_gw=true; ok "gateway /healthz"; fi
    if [[ "$ok_ui" == false ]] && curl -fsS --max-time 3 -o /dev/null "http://127.0.0.1:${dash_port}/" 2>/dev/null; then ok_ui=true; ok "dashboard"; fi
    if [[ "$ok_gw" == true && "$ok_ui" == true ]]; then break; fi
    sleep 3
  done
  # through Traefik, the way users will reach it
  base="http://127.0.0.1:$(grep -E '^HTTP_PORT=' .env | cut -d= -f2- || true)"
  [[ "$base" == "http://127.0.0.1:" ]] && base="http://127.0.0.1"
  local host_hdr="localhost"
  if [[ "$IP_ONLY" != true ]]; then host_hdr="$(grep -E '^MANAGEMENT_DOMAIN=' .env | cut -d= -f2-)"; fi
  if curl -fsS --max-time 5 -H "Host: ${host_hdr}" -o /dev/null "${base}/api/v1/auth/bootstrap/status" 2>/dev/null ||
     curl -sS --max-time 5 -H "Host: ${host_hdr}" -o /dev/null -w '%{http_code}' "${base}/healthz" 2>/dev/null | grep -qE '^(2|3|4)'; then
    ok_edge=true; ok "traefik routes ${host_hdr} -> gateway"
  fi

  if [[ "$ok_gw" != true || "$ok_ui" != true ]]; then
    warn "not everything came up in time. Unhealthy / restarting containers:"
    (bash ./scripts/jp-compose.sh ps 2>/dev/null || true) | grep -Ev ' healthy| Up .*\(healthy\)' | head -20 || true
    warn "inspect with:  cd $ROOT_DIR && docker compose logs --tail=80 <service>"
    return 1
  fi
  [[ "$ok_edge" == true ]] || warn "Traefik routing check inconclusive (see: docker compose logs traefik)"
  return 0
}

print_summary() {
  local panel url
  panel="$(grep -E '^MANAGEMENT_DOMAIN=' "$ROOT_DIR/.env" | cut -d= -f2-)"
  url="$(grep -E '^PUBLIC_BASE_URL=' "$ROOT_DIR/.env" | cut -d= -f2-)"
  echo
  echo "=========================================================="
  echo "  JP Cloud is running"
  echo "=========================================================="
  echo "  Open:        ${url}"
  if [[ "$IP_ONLY" == true ]]; then echo "               http://${PUBLIC_IP}:3000   (direct, same panel)"; fi
  echo "  Next step:   register the FIRST account - it becomes the platform administrator."
  echo "               Registration is '$(grep -E '^REGISTRATION_MODE=' "$ROOT_DIR/.env" | cut -d= -f2-)' afterwards (invite-only unless changed)."
  echo "  Config:      $ROOT_DIR/.env   (chmod 600; BACK IT UP - SECRETS_MASTER_KEY cannot be recovered)"
  echo "  Operate:     scripts/backup.sh | scripts/upgrade.sh | scripts/jp-up.sh --monitoring | ./uninstall.sh"
  echo "  Traefik UI:  ssh -L 8080:127.0.0.1:8080 <server>  ->  http://localhost:8080/dashboard/  (user admin, password: TRAEFIK_DASHBOARD_PASSWORD in .env)"
  if [[ "$IP_ONLY" != true && -n "$panel" ]]; then
    echo "  DNS needed:  A ${panel} and wildcard A *.$(grep -E '^ROOT_DOMAIN=' "$ROOT_DIR/.env" | cut -d= -f2-) -> ${PUBLIC_IP}"
  fi
  echo "  Cloud NSG:   allow inbound 80/443$([[ "$IP_ONLY" == true ]] && echo '/3000') in your cloud provider's firewall as well."
  echo "=========================================================="
}

main() {
  parse_args "$@"
  preflight
  install_base_tools
  setup_swap
  ensure_docker
  tune_kernel
  locate_repo
  decide_mode
  configure_firewall
  isolate_host
  write_env
  check_dns
  start_stack
  if verify_stack; then print_summary; else die "installation finished but the platform is not healthy yet"; fi
}

main "$@"
