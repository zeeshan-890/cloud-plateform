#!/usr/bin/env bash
# Fails when the Go code and the deployment config disagree about environment variables.
#
#   1. every env var a service reads (os.Getenv / getEnv / GetEnvBool / ... with a literal name) must be present in that
#      service's `environment:` in infra/compose/docker-compose.yml (rendered with `docker compose config`)
#   2. every variable the compose files interpolate (${NAME...}) must be documented in .env.example
#   3. (warning only) env keys defined in compose that no code reads
#
# Variables that are intentionally NOT passed to a service are listed in scripts/verify-env.allow
# ("<service-dir> <VAR>" or "* <VAR>" per line, # comments allowed).
#
# Needs: docker (compose v2; the engine does NOT need to run), python3.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PY=python3
if ! python3 -c 'import sys' >/dev/null 2>&1; then PY=python; fi

TMP_JSON="$(mktemp)"
trap 'rm -f "$TMP_JSON"' EXIT
docker compose -f infra/compose/docker-compose.yml -f infra/compose/docker-compose.dev.yml \
  --env-file infra/compose/dev.env --profile monitoring --profile dev config --format json >"$TMP_JSON"

"$PY" - "$ROOT" "$TMP_JSON" <<'PYEOF'
import json, os, re, sys

root, cfg_path = sys.argv[1], sys.argv[2]
cfg = json.load(open(cfg_path, encoding="utf-8"))
services = cfg["services"]

# directory under services/ -> compose service name
alias = {"registry": "registry-api"}

call_re = re.compile(r'[A-Za-z_.]*(?:[Ee]nv|Getenv|LookupEnv)[A-Za-z0-9_]*\(([^()]*(?:\([^()]*\)[^()]*)*)\)')
name_re = re.compile(r'"([A-Z][A-Z0-9_]{2,})"')
const_re = re.compile(r'"((?:[A-Z][A-Z0-9]*_)+[A-Z0-9]+)"')  # only used for env-name constants passed indirectly

def go_files(base):
    for dp, dn, fn in os.walk(base):
        dn[:] = [d for d in dn if d not in ("node_modules", "vendor", ".git")]
        for f in fn:
            if f.endswith(".go") and not f.endswith("_test.go"):
                yield os.path.join(dp, f)

def vars_in(base):
    found = {}
    for p in go_files(base):
        try:
            txt = open(p, encoding="utf-8", errors="replace").read()
        except OSError:
            continue
        for m in call_re.finditer(txt):
            for n in name_re.findall(m.group(1)):
                found.setdefault(n, os.path.relpath(p, root))
    return found

allow = set()
allow_file = os.path.join(root, "scripts", "verify-env.allow")
if os.path.exists(allow_file):
    for line in open(allow_file, encoding="utf-8"):
        line = line.split("#", 1)[0].strip()
        if line:
            a, b = line.split()
            allow.add((a, b))

errors, warnings = [], []
read_anywhere = set()

svc_root = os.path.join(root, "services")
common_vars = vars_in(os.path.join(root, "packages", "go-common"))
common_vars.update(vars_in(os.path.join(root, "packages", "events")))

for d in sorted(os.listdir(svc_root)):
    base = os.path.join(svc_root, d)
    if not os.path.isdir(base) or not os.path.exists(os.path.join(base, "go.mod")):
        continue
    if d == "node":
        continue  # the node agent runs as a systemd service on extra servers (installed by the join command), not in compose
    name = alias.get(d, d)
    if name not in services:
        errors.append(f"services/{d}: no matching compose service '{name}'")
        continue
    env = services[name].get("environment") or {}
    wanted = vars_in(base)
    read_anywhere.update(wanted)
    for v, src in sorted(wanted.items()):
        if v in env or ("*", v) in allow or (d, v) in allow:
            continue
        errors.append(f"{name}: reads {v} ({src}) but compose does not set it")

# shared packages: vars they read must reach every Go service that links them. Check the common block is present on gateway.
gw_env = services["gateway"].get("environment") or {}
for v, src in sorted(common_vars.items()):
    read_anywhere.add(v)
    if v in gw_env or ("*", v) in allow:
        continue
    errors.append(f"gateway: shared package reads {v} ({src}) but compose does not set it")

# compose interpolations must be documented
interp = set()
for fn in sorted(os.listdir(os.path.join(root, "infra", "compose"))):
    if fn.endswith(".yml"):
        txt = open(os.path.join(root, "infra", "compose", fn), encoding="utf-8").read()
        interp.update(re.findall(r"\$\{([A-Z][A-Z0-9_]*)[:}?-]", txt))
example = open(os.path.join(root, ".env.example"), encoding="utf-8").read()
documented = set(re.findall(r"^#?\s*([A-Z][A-Z0-9_]*)=", example, re.M))
for v in sorted(interp - documented):
    errors.append(f".env.example: {v} is used by compose but not documented")

# dead keys
for name, s in sorted(services.items()):
    for k in sorted((s.get("environment") or {}).keys()):
        if k in read_anywhere or k in ("PORT", "MIGRATIONS_DIR", "HOME", "DOCKER_CONFIG", "TRIVY_CACHE_DIR", "GOMEMLIMIT",
                                       "WORK_DIR", "TLS_ENABLED", "ACME_EMAIL", "ACME_STAGING", "MANAGEMENT_DOMAIN",
                                       "TRAEFIK_LOG_LEVEL", "REDIS_PASSWORD", "REDISCLI_AUTH"):
            continue
        if name in ("postgres", "redis", "minio", "registry", "grafana", "docker-socket-proxy") or k.startswith(("POSTGRES_", "MINIO_ROOT", "REGISTRY_", "GF_", "MINIO_")):
            continue
        warnings.append(f"{name}: sets {k} but no Go code reads it")

for w in warnings:
    print("warning:", w)
for e in errors:
    print("ERROR:", e)
print(f"verify-env: {len(errors)} error(s), {len(warnings)} warning(s)")
sys.exit(1 if errors else 0)
PYEOF
