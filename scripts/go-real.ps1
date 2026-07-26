# Switch jp to REAL modes and bring the stack up.
# - Docker BuildKit builds (worker + docker.sock)
# - Docker runtime
# - MinIO storage + schema Postgres DBs
# - Live metrics
#
# Usage:
#   .\scripts\go-real.ps1
#   .\scripts\go-real.ps1 -Profiles platform,ui,data,monitoring

param(
    [string]$Profiles = "platform,ui,data",
    [ValidateSet("auto", "small", "medium", "large", "xlarge")]
    [string]$Size = "auto"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

Write-Host "==> Enabling REAL platform modes"

# Ensure .env exists and force real flags
if (-not (Test-Path "$Root\.env")) {
    Copy-Item "$Root\.env.example" "$Root\.env"
}

function Set-EnvKey([string]$path, [string]$key, [string]$value) {
    $lines = Get-Content $path
    $found = $false
    $out = foreach ($line in $lines) {
        if ($line -match "^\s*$key=") {
            $found = $true
            "$key=$value"
        } else {
            $line
        }
    }
    if (-not $found) { $out += "$key=$value" }
    $out | Set-Content $path -Encoding utf8
}

$envPath = "$Root\.env"
Set-EnvKey $envPath "BUILD_MODE" "docker"
Set-EnvKey $envPath "SIMULATE_BUILD" "false"
Set-EnvKey $envPath "RUNTIME_MODE" "docker"
Set-EnvKey $envPath "STORAGE_MODE" "minio"
Set-EnvKey $envPath "DB_MODE" "schema"
Set-EnvKey $envPath "METRICS_MODE" "live"
Set-EnvKey $envPath "DOMAIN_DNS_STUB" "false"
Set-EnvKey $envPath "CERT_SIMULATE" "false"
Set-EnvKey $envPath "REGISTRY_PUSH_URL" "host.docker.internal:5000"
Set-EnvKey $envPath "REGISTRY_HOST" "host.docker.internal:5000"

& "$Root\scripts\size-host.ps1" -Size $Size

# Force real into .env.resources (override small-tier simulate defaults)
Set-EnvKey "$Root\.env.resources" "BUILD_MODE" "docker"
Set-EnvKey "$Root\.env.resources" "SIMULATE_BUILD" "false"
Set-EnvKey "$Root\.env.resources" "RUNTIME_MODE" "docker"
Set-EnvKey "$Root\.env.resources" "METRICS_MODE" "live"
Set-EnvKey "$Root\.env.resources" "STORAGE_MODE" "minio"
Set-EnvKey "$Root\.env.resources" "DB_MODE" "schema"

Write-Host ""
Write-Host "Docker Desktop: add insecure registry localhost:5000 if push fails:"
Write-Host '  Settings → Docker Engine → "insecure-registries": ["localhost:5000","host.docker.internal:5000"]'
Write-Host ""

& "$Root\scripts\jp-up.ps1" -Size $Size -Profiles $Profiles

Write-Host ""
Write-Host "REAL mode stack requested. Verify:"
Write-Host "  curl http://localhost:8000/healthz"
Write-Host "  jp deploy --project <id>   # should queue a Docker build"
