# Installation

JP runs on a single Linux server (x86_64 or arm64) with Docker. Tested target: Oracle Linux 9 on aarch64 (2 vCPU / 11 GB).
Supported by the installer: Oracle Linux / RHEL / Rocky / Alma / CentOS / Fedora, Debian, Ubuntu. macOS and Windows are
for local development only (Docker Desktop).

## Requirements

| | |
|---|---|
| RAM / CPU | 4 GB / 2 vCPU minimum (all 21 services + Postgres, Redis, MinIO, registry, Traefik run by default; ~1.8 GB idle). Add `--swap 2G` on small hosts. |
| Disk | 20 GB+ (image builds, registry, database) |
| Network | inbound 80 and 443 (plus 3000 for `--ip-only`) from the internet, outbound HTTPS (GitHub, Docker Hub, Let's Encrypt, Trivy DB) |
| DNS (domain mode) | `A panel.<domain>` and wildcard `A *.<root-domain>` -> server IP |
| Cloud firewall | the installer configures the *host* firewall only. Open the same ports in your provider (Oracle VCN security list / AWS SG). Port 22 is never touched. |

## Install

```bash
# domain mode: Let's Encrypt certificates, panel at https://panel.example.com, apps at https://<project>-<org>.apps.example.com
curl -fsSL https://raw.githubusercontent.com/zeeshan-890/cloud-plateform/main/install.sh | \
  bash -s -- --domain apps.example.com --panel-domain panel.example.com --email you@example.com

# IP mode: http only, apps at <project>-<org>.<ip-with-dashes>.sslip.io, panel at http://<ip> and http://<ip>:3000
curl -fsSL .../install.sh | bash -s -- --ip-only
```

From a checkout: `./install.sh [options]`. Options (`--help` lists all):

| Option | Meaning |
|---|---|
| `--domain D` | `ROOT_DOMAIN`. Apps get `<project>-<org>.D`. Needs wildcard DNS. |
| `--panel-domain H` | `MANAGEMENT_DOMAIN` (default `panel.D`) |
| `--email E` | Let's Encrypt contact; required when TLS is on |
| `--no-tls` | domain mode over plain http |
| `--ip-only` | no domain: `ROOT_DOMAIN=<ip-dashes>.sslip.io`, TLS off, publishes the dashboard on 0.0.0.0:3000, opens 3000 |
| `--registration closed\|invite\|open` | after the first admin exists (default `invite`) |
| `--tier`, `--monitoring`, `--swap`, `--skip-docker`, `--skip-firewall`, `--no-update`, `--ref`, `--dir`, `-y` | see `--help` |

Without flags the script asks (when a terminal is available); with `-y` and no `--domain` it uses `--ip-only`.

What the installer does, in order: checks OS/arch/RAM/disk -> installs git/curl/openssl -> installs Docker CE + compose +
buildx (on Oracle Linux/RHEL it removes the conflicting `podman-docker`/`runc` packages via `dnf --allowerasing`; falls back to
get.docker.com) and enables the service -> sets `vm.overcommit_memory=1` -> clones or updates the repository -> opens the
firewall (firewalld, else ufw, else iptables; for iptables it inserts `ACCEPT` rules *before* the stock `REJECT` rule that Oracle
Cloud images ship and persists them with netfilter-persistent / `iptables-save`) -> writes `.env` -> builds and starts the stack
-> waits for gateway `/healthz`, the dashboard and the Traefik route -> prints the URL.

### The `.env` file

* generated with `openssl rand`: `JWT_SECRET` (48 hex chars), `INTERNAL_TOKEN`, `SECRETS_MASTER_KEY` / `AI_ENC_KEY` /
  `IDENTITY_ENC_KEY` (base64 of 32 random bytes), database / Redis / registry / MinIO / Grafana / Traefik-dashboard credentials.
  Nothing secret is printed.
* `chmod 600`. The installer **never overwrites** an existing `.env`; on re-run it only appends missing keys (and fills empty
  secrets). Domain flags do not change an existing file - edit it by hand.
* **Back it up.** Without `SECRETS_MASTER_KEY` every stored project secret is unreadable. `scripts/backup.sh` includes it.
* Reference of every variable: [ENVIRONMENT.md](ENVIRONMENT.md).

Re-running `./install.sh` is safe: it pulls updates, adds missing keys and reapplies compose. For upgrades prefer
`scripts/upgrade.sh` (backup, ordered restart, rollback).

## First login

Open the printed URL and register: the **first account becomes the platform administrator** (set `BOOTSTRAP_TOKEN` in `.env`
beforehand if you want to require a token for that step). Afterwards registration follows `REGISTRATION_MODE`
(`invite` by default: new users need an invitation from an org admin; `closed` disables it; `open` allows anyone).

Optional integrations are configured in `.env` (then `scripts/jp-compose.sh up -d`): GitHub App (`GITHUB_APP_*`),
SMTP (`SMTP_*`), AI provider keys (also per organization in the dashboard). See [ENVIRONMENT.md](ENVIRONMENT.md).

## Verify

```bash
scripts/jp-compose.sh ps                          # every container should be healthy
curl -fsS http://127.0.0.1:8000/healthz           # gateway (loopback only)
curl -fsS http://127.0.0.1/api/v1/auth/bootstrap/status -H 'Host: <panel-host or localhost>'   # through Traefik
```

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `required variable X is missing a value` | `.env` incomplete: `./install.sh` again (adds missing keys) or set it |
| runtime/worker `permission denied` on `/var/run/docker.sock` | `DOCKER_GID` in `.env` must be the socket's group: `stat -c %g /var/run/docker.sock` |
| site unreachable from outside, works on the server | cloud firewall / security list blocks 80, 443 (or 3000) |
| certificate not issued | DNS for the host must already point at this server; check `scripts/jp-compose.sh logs traefik`; try `ACME_STAGING=true` first |
| `docker pull 127.0.0.1:5000/...` fails | registry unhealthy: `scripts/jp-compose.sh logs registry`; Docker treats 127.0.0.0/8 registries as insecure by default, nothing to configure |
| builds OOM-killed | 4 GB host without swap: `--swap 2G`; lower `WORKER_CONCURRENCY` |
| dnf conflict on Oracle Linux | the installer removes `podman-docker`/`runc`; if you must keep podman use `--skip-docker` and install Docker yourself |
| SELinux denials | Docker CE ships with SELinux integration off by default; if you enabled `selinux-enabled` add `:z` labels or disable it for the socket mounts |

## Uninstall

`./uninstall.sh` removes platform and tenant containers/networks and keeps data volumes and `.env`;
`./uninstall.sh --purge-data` deletes everything (asks for confirmation). Docker, firewall rules and the checkout are left alone.
