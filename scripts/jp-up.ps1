# Starts jp with host-aware resource overlay + worker scale.
# Usage:
#   .\scripts\jp-up.ps1
#   .\scripts\jp-up.ps1 -Profiles platform,ui,data
#   .\scripts\jp-up.ps1 -Size large -Profiles platform,ui,data,monitoring

param(
    [ValidateSet("auto", "small", "medium", "large", "xlarge")]
    [string]$Size = "auto",
    [string]$Profiles = "",
    [switch]$NoBuild
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root

& "$Root\scripts\size-host.ps1" -Size $Size

if (-not (Test-Path "$Root\.env")) {
    if (Test-Path "$Root\.env.example") {
        Copy-Item "$Root\.env.example" "$Root\.env"
        Write-Host "created .env from .env.example"
    } else {
        throw "missing .env — copy .env.example first"
    }
}

# Merge .env.resources into a temp env file (resources override example defaults)
$merged = Join-Path $Root ".env.merged"
Get-Content "$Root\.env" | Set-Content $merged
Add-Content $merged ""
Add-Content $merged "# --- from .env.resources ---"
Get-Content "$Root\.env.resources" | Where-Object { $_ -and $_ -notmatch '^\s*#' } | ForEach-Object {
    # strip duplicate keys from earlier by appending (compose last-wins via --env-file order)
    $_
} | Add-Content $merged

$res = @{}
Get-Content "$Root\.env.resources" | ForEach-Object {
    if ($_ -match '^\s*([A-Za-z0-9_]+)=(.*)$') { $res[$Matches[1]] = $Matches[2].Trim() }
}

$tier = $res["JP_SIZE"]
$overlay = $res["COMPOSE_SIZING_FILE"]
$replicas = [int]($res["WORKER_REPLICAS"])
if (-not $Profiles) { $Profiles = $res["JP_SUGGESTED_PROFILES"] }

$composeArgs = @(
    "-f", "infra/compose/docker-compose.yml",
    "-f", $overlay,
    "--env-file", ".env",
    "--env-file", ".env.resources"
)

$profileArgs = @()
foreach ($p in ($Profiles -split ",")) {
    $p = $p.Trim()
    if ($p -and $p -ne "core") {
        $profileArgs += @("--profile", $p)
    }
}

$upArgs = @("compose") + $composeArgs + $profileArgs + @("up", "-d")
if (-not $NoBuild) { $upArgs += "--build" }
if ($replicas -gt 1) { $upArgs += @("--scale", "worker=$replicas") }

Write-Host "docker $($upArgs -join ' ')"
& docker @upArgs
if ($LASTEXITCODE -ne 0) { throw "docker compose failed (is Docker Desktop running?)" }

Write-Host ""
Write-Host "jp is up — size=$tier workers×$replicas profiles=$Profiles"
Write-Host "API: http://localhost:8000/api/v1"
