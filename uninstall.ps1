# JP Cloud - Windows uninstaller (local development).
#   .\uninstall.ps1               stop + remove containers (platform and tenant), keep data volumes and .env
#   .\uninstall.ps1 -PurgeData    also delete volumes and .env
#   .\uninstall.ps1 -Yes          no confirmation
param([switch]$PurgeData, [switch]$Yes)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

if ($PurgeData -and -not $Yes) {
    Write-Warning "-PurgeData permanently deletes the database, storage, certificates and .env."
    $a = Read-Host "Type 'delete everything' to continue"
    if ($a -ne "delete everything") { Write-Host "aborted"; exit 0 }
}

Write-Host "==> removing tenant containers (label jp.managed=true)"
$tc = docker ps -aq --filter "label=jp.managed=true"
if ($tc) { docker rm -f $tc | Out-Null }

Write-Host "==> stopping the platform stack"
$down = @("compose", "-f", "infra/compose/docker-compose.yml", "-f", "infra/compose/docker-compose.dev.yml", "-f", "infra/compose/docker-compose.e2e.yml",
          "--env-file", "infra/compose/dev.env")
if (Test-Path "$Root\.env") { $down += @("--env-file", ".env") }
$down += @("--profile", "*", "down", "--remove-orphans")
if ($PurgeData) { $down += "--volumes" }
& docker @down

Write-Host "==> removing tenant networks (jp_p_*)"
foreach ($n in (docker network ls -q --filter "name=jp_p_")) { docker network rm $n 2>$null | Out-Null }

if ($PurgeData) {
    foreach ($f in ".env", ".env.resources") { Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $Root $f) }
    Write-Host "All platform data and configuration removed." -ForegroundColor Yellow
} else {
    Write-Host "Containers removed. Data volumes and .env kept. Start again with .\install.ps1" -ForegroundColor Green
}
