# ==============================================================================
# JP Cloud Platform — Canonical Windows / PowerShell Uninstaller
# Gracefully stops platform services. Preserves persistent data by default
# unless -PurgeData is explicitly specified.
#
# Usage:
#   .\uninstall.ps1               # Preserves databases and volume data
#   .\uninstall.ps1 -PurgeData    # Completely removes data volumes and .env
# ==============================================================================
param(
    [switch]$PurgeData,
    [switch]$Force
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "    JP Cloud Platform — Uninstaller" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""

if ($PurgeData -and -not $Force) {
    Write-Warning "You have specified -PurgeData."
    Write-Warning "This will PERMANENTLY DELETE all database volumes, user data, and configuration!"
    $confirm = Read-Host "Are you sure you want to proceed? [y/N]"
    if ($confirm -notmatch '^[Yy]$') {
        Write-Host "Uninstall aborted."
        exit 0
    }
}

Write-Host "==> Stopping JP containers and services..."
if (Get-Command docker -ErrorAction SilentlyContinue) {
    if (Test-Path "$Root\infra\compose\docker-compose.yml") {
        docker compose -f "$Root\infra\compose\docker-compose.yml" down --remove-orphans
    }
}

if ($PurgeData) {
    Write-Host "==> Purging all persistent Docker volumes..."
    if (Get-Command docker -ErrorAction SilentlyContinue) {
        docker compose -f "$Root\infra\compose\docker-compose.yml" down -v --remove-orphans
    }

    Write-Host "==> Removing environment configuration..."
    Remove-Item -Path "$Root\.env", "$Root\.env.resources" -Force -ErrorAction SilentlyContinue
    Write-Host "All platform data and volumes have been permanently removed." -ForegroundColor Yellow
} else {
    Write-Host ""
    Write-Host "==========================================================" -ForegroundColor Green
    Write-Host "  JP platform containers have been stopped and removed." -ForegroundColor Green
    Write-Host "  Persistent volumes (databases, storage, keys) PRESERVED." -ForegroundColor Green
    Write-Host "==========================================================" -ForegroundColor Green
    Write-Host ""
    Write-Host "  To start the platform again: .\install.ps1"
    Write-Host "  To completely remove all data: .\uninstall.ps1 -PurgeData"
}
