# Run all Go module tests natively in PowerShell
$ErrorActionPreference = "Stop"
$rootDir = (Get-Item $PSScriptRoot).Parent.FullName

$env:GOTMPDIR = Join-Path $rootDir ".tmp"
if (-not (Test-Path $env:GOTMPDIR)) {
    New-Item -ItemType Directory -Path $env:GOTMPDIR -Force | Out-Null
}

Write-Host "=== Running Go Test Suite across all modules ===" -ForegroundColor Cyan

$modules = Get-Content (Join-Path $rootDir "go.work") | Where-Object { $_ -match '^\s*\./' } | ForEach-Object { $_.Trim() }

foreach ($mod in $modules) {
    Write-Host "=============================================" -ForegroundColor Yellow
    Write-Host "Testing module: $mod" -ForegroundColor Yellow
    Write-Host "=============================================" -ForegroundColor Yellow
    
    $modDir = Join-Path $rootDir $mod
    Push-Location $modDir
    try {
        go test -v ./...
        if ($LASTEXITCODE -ne 0) {
            throw "Tests failed in module $mod"
        }
    } finally {
        Pop-Location
    }
}

Write-Host "=============================================" -ForegroundColor Green
Write-Host "All Go modules tested successfully!" -ForegroundColor Green
Write-Host "=============================================" -ForegroundColor Green
