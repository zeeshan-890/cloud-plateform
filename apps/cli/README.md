# jp CLI

Command-line interface for the Cloud Platform.

## Install

From this directory:

```bash
# Install into $(go env GOPATH)/bin
go install github.com/jp-cloud/jp@latest   # once published
go install .

# Or build a local binary named jp
go build -o jp .
```

On Windows (PowerShell):

```powershell
cd apps/cli
go build -o jp.exe .
```

Ensure `$(go env GOPATH)/bin` is on your `PATH` if using `go install`.

## Configuration

Config is stored at `~/.jp/config.json` (override directory with `JP_CONFIG_DIR`).

Fields:

| Field | Description |
|-------|-------------|
| `access_token` | Bearer access token |
| `refresh_token` | Refresh token |
| `api_url` | API base URL (default `http://localhost:8000/api/v1`) |
| `current_org_id` | Active organization for `jp projects` |

```bash
jp config set api-url http://localhost:8000/api/v1
jp config get
```

## Working commands

| Command | Description |
|---------|-------------|
| `jp login` | Interactive email/password login |
| `jp login --token <pat>` | Login with a personal access token |
| `jp logout` | Clear credentials (best-effort server logout) |
| `jp whoami` | Show current user |
| `jp orgs` | List organizations |
| `jp org use <slug\|id>` | Set current organization |
| `jp projects` | List projects in current org |
| `jp init [name]` | Scaffold `jp.yaml` in cwd |
| `jp config set api-url <url>` | Set API base URL |
| `jp config get` | Show config (tokens masked) |

## Stubs (future phases)

These print a clear “requires Phase N” message and exit successfully:

`deploy`, `rollback`, `status`, `logs`, `metrics`, `trace`, `domains`, `env`, `secrets`, `storage`, `db`, `ai`, `builds`

## Examples

```bash
jp config set api-url http://localhost:8000/api/v1
jp login
jp orgs
jp org use acme
jp projects
jp init my-app
```

## Development

```bash
cd apps/cli
go mod tidy
go build -o jp .
./jp --help
```
