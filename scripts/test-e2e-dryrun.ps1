<#
.SYNOPSIS
    back-EZ End-to-End DryRun Full Deployment Verification (Task 4.5)
.DESCRIPTION
    Executes automated end-to-end simulation of 1 Master + 20 Targets:
    - Full Lifecycle: WOL -> Snapshot -> PE -> Multicast -> Verify -> Switch -> Restore -> Online
    - USB PE Rescue Workflow
    - 3 Failure Injections: Connection Drop, Checksum Failure, Command Expiration
#>

[CmdletBinding()]
param(
    [switch]$VerboseOutput = $false
)

$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  back-EZ End-to-End DryRun Verification Suite (Task 4.5)  " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

Write-Host "Running 20-Node Full Deployment Lifecycle & Failure Injections..." -ForegroundColor Yellow
$sw = [System.Diagnostics.Stopwatch]::StartNew()

go test -v -run "TestE2E_" ./cmd/mockagent/...

if ($LASTEXITCODE -ne 0) {
    Write-Error "E2E DryRun integration test failed with exit code $LASTEXITCODE."
    exit 1
}

$sw.Stop()
$elapsedSec = [math]::Round($sw.Elapsed.TotalSeconds, 2)
Write-Host "`n[SUCCESS] E2E DryRun Integration Suite passed in $elapsedSec seconds." -ForegroundColor Green
Write-Host "Verified 20-node multicast simulation, rescue workflow, and 3 failure injections." -ForegroundColor Green

