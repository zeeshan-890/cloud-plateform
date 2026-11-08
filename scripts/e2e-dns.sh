#!/usr/bin/env bash
# Edit the e2e test DNS zone served by the `dns` (CoreDNS) service of docker-compose.e2e.yml.
#
#   scripts/e2e-dns.sh set  <fqdn> <A|AAAA|CNAME|TXT> <value> [zone]   add/replace a record
#   scripts/e2e-dns.sh del  <fqdn> [type] [zone]                       remove records (all types if omitted)
#   scripts/e2e-dns.sh show [zone]                                      print the zone file
#
# zone defaults to $E2E_ZONE or "e2e.test". Zone files live in infra/e2e/dns/zones/db.<zone>;
# CoreDNS rescans that directory every 2 seconds. Each edit is atomic (tmp + rename) and bumps the SOA serial.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ZONES="${E2E_ZONES_DIR:-$ROOT/infra/e2e/dns/zones}"
DEFAULT_ZONE="${E2E_ZONE:-e2e.test}"

die() { echo "error: $*" >&2; exit 1; }
usage() { sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 2; }

zone_file() { echo "$ZONES/db.$1"; }

ensure_zone() {
  local zone="$1" f; f="$(zone_file "$zone")"
  if [[ ! -f "$f" ]]; then
    cat >"$f" <<ZONE
\$ORIGIN ${zone}.
\$TTL 5
@   IN SOA ns.${zone}. hostmaster.${zone}. ( 1 5 5 3600 5 )
@   IN NS  ns.${zone}.
ns  IN A   127.0.0.1
ZONE
  fi
}

# rewrite: strip matching records (stdin->stdout) then bump serial
rewrite() {
  local zone="$1" name="$2" type="$3" add="$4" f tmp serial
  f="$(zone_file "$zone")"; tmp="$f.tmp.$$"; serial="$(date +%s)"
  awk -v name="${name}." -v type="$type" -v serial="$serial" '
    /IN SOA/ { sub(/\( *[0-9]+ /, "( " serial " ") }
    { n=$1; t=$4
      if (n == name && (type == "" || t == type)) next
      print }' "$f" >"$tmp"
  [[ -n "$add" ]] && printf '%s\n' "$add" >>"$tmp"
  mv -f "$tmp" "$f"
}

valid_name() { [[ "$1" =~ ^[A-Za-z0-9_]([A-Za-z0-9_.-]{0,251}[A-Za-z0-9])?$ ]] || die "invalid name: $1"; }

cmd="${1:-}"; shift || true
case "$cmd" in
  set)
    [[ $# -ge 3 ]] || usage
    fqdn="${1%.}"; type="${2^^}"; value="$3"; zone="${4:-$DEFAULT_ZONE}"
    valid_name "$fqdn"
    [[ "$fqdn" == "$zone" || "$fqdn" == *".$zone" ]] || die "$fqdn is not inside zone $zone"
    case "$type" in
      A|AAAA) rec="${fqdn}. 5 IN $type $value" ;;
      CNAME)  rec="${fqdn}. 5 IN CNAME ${value%.}." ;;
      TXT)    rec="${fqdn}. 5 IN TXT \"${value//\"/}\"" ;;
      *) die "unsupported type $type" ;;
    esac
    mkdir -p "$ZONES"; ensure_zone "$zone"
    rewrite "$zone" "$fqdn" "$type" "$rec"
    echo "set $rec" ;;
  del)
    [[ $# -ge 1 ]] || usage
    fqdn="${1%.}"; type="${2:-}"; type="${type^^}"; zone="${3:-$DEFAULT_ZONE}"
    valid_name "$fqdn"
    [[ -f "$(zone_file "$zone")" ]] || die "no such zone file for $zone"
    rewrite "$zone" "$fqdn" "$type" ""
    echo "deleted ${fqdn} ${type:-*}" ;;
  show)
    cat "$(zone_file "${1:-$DEFAULT_ZONE}")" ;;
  *) usage ;;
esac
