# Run vet + race tests for every module in go.work (same loop as CI). -Short skips -race (no CGO needed).
param([switch]$Short)
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $Root
$mods = (Get-Content go.work) | ForEach-Object { $_.Trim() } | Where-Object { $_ -match '^\./' }
$fail = 0
foreach ($m in $mods) {
    Write-Host "=== $m"
    Push-Location (Join-Path $Root $m)
    try {
        go vet ./...
        if ($LASTEXITCODE -ne 0) { $fail = 1; Write-Warning "vet failed: $m"; continue }
        if ($Short) { go test -count=1 ./... } else { go test -race -count=1 ./... }
        if ($LASTEXITCODE -ne 0) { $fail = 1; Write-Warning "tests failed: $m" }
    } finally { Pop-Location }
}
exit $fail
