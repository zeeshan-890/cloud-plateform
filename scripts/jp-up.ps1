# Start (or update) the stack with the right compose files for this host (Windows / local development).
#   .\scripts\jp-up.ps1                 uses .env if present, else infra/compose/dev.env defaults (-Dev implied)
#   .\scripts\jp-up.ps1 -Dev            force the development override (publishes datastores on 127.0.0.1)
#   .\scripts\jp-up.ps1 -Monitoring     start prometheus/grafana/loki/tempo/promtail too
#   .\scripts\jp-up.ps1 -E2E            add the test DNS server
#   .\scripts\jp-up.ps1 -Size medium -NoBuild
param(
    [ValidateSet("auto", "small", "medium", "large", "xlarge")]
    [string]$Size = "auto",
    [switch]$Dev,
    [switch]$Monitoring,
    [switch]$E2E,
    [switch]$NoBuild
)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

& "$Root\scripts\size-host.ps1" -Size $Size

$hasEnv = Test-Path "$Root\.env"
if (-not $hasEnv) { $Dev = $true }

$sizing = (Select-String -Path "$Root\.env.resources" -Pattern '^COMPOSE_SIZING_FILE=(.*)$').Matches[0].Groups[1].Value
$replicas = [int](Select-String -Path "$Root\.env.resources" -Pattern '^WORKER_REPLICAS=(.*)$').Matches[0].Groups[1].Value

$composeArgs = @("compose", "-f", "infra/compose/docker-compose.yml", "-f", $sizing)
if ($Dev) { $composeArgs += @("-f", "infra/compose/docker-compose.dev.yml", "--env-file", "infra/compose/dev.env") }
if ($E2E) { $composeArgs += @("-f", "infra/compose/docker-compose.e2e.yml") }
if ($hasEnv) { $composeArgs += @("--env-file", ".env") }
$composeArgs += @("--env-file", ".env.resources")
if ($Monitoring) { $composeArgs += @("--profile", "monitoring") }
if ($Dev) { $composeArgs += @("--profile", "dev") }
$composeArgs += @("up", "-d", "--remove-orphans")
if (-not $NoBuild) { $composeArgs += "--build" }
if ($replicas -gt 1) { $composeArgs += @("--scale", "worker=$replicas") }

Write-Host "docker $($composeArgs -join ' ')"
& docker @composeArgs
if ($LASTEXITCODE -ne 0) { throw "docker compose failed (exit $LASTEXITCODE)" }
