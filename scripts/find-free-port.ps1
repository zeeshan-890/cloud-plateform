# ==============================================================================
# Helper to find a free, unallocated port starting from a default port on Windows.
# Usage: .\scripts\find-free-port.ps1 -Port 80
# ==============================================================================
param(
  [int]$Port = 80
)

function Test-PortBusy([int]$p) {
  $inUse = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
  if ($null -ne $inUse) { return $true }

  $dockerPorts = docker ps --format '{{.Ports}}' 2>$null
  if ($dockerPorts -match "[:\]]$p->") { return $true }

  return $false
}

$candidate = $Port
while (Test-PortBusy $candidate) {
  $candidate++
}

Write-Output $candidate
