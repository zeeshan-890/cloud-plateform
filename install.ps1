# ==============================================================================
# JP Cloud Platform — Canonical Windows / PowerShell Single-Server Installer
# ==============================================================================
param(
    [ValidateSet("auto", "lite", "small", "medium", "large", "xlarge")]
    [string]$Size = "auto"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "    JP Cloud Platform — Self-Hosted Installation" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""

# Check Docker
Write-Host "==> Checking prerequisites..."
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Write-Error "Docker is not installed or not in PATH. Please install Docker Desktop."
}

# Sizing
Write-Host "==> Sizing host resources..."
& "$Root\scripts\size-host.ps1" -Size $Size

# Environment
if (-not (Test-Path "$Root\.env")) {
    Write-Host "==> Generating platform configuration and cryptographic secrets..."
    Copy-Item "$Root\.env.example" "$Root\.env"
}

# Launch
Write-Host "==> Launching platform..."
& "$Root\scripts\jp-up.ps1"

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "  JP Cloud Platform is starting up." -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Green
Write-Host "  Web Console: http://localhost:3000"
Write-Host "  API Gateway: http://localhost:8000"
Write-Host ""
