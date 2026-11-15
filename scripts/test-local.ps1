<#
.SYNOPSIS
    back-EZ local restore-path test entry point (Phase 0, 2026-11-12).

.DESCRIPTION
    Runs the Go tests for the locally maintained restore path only:
      cmd/gui, internal/agent, internal/workers/vhdx,
      internal/workers/homesku, internal/protocol

    The control console (internal/hub, web/, cmd/mockagent, cmd/gen-ts, ...)
    is frozen: see TASKS.md "現階段範圍（2026-11-12）" and DECISIONS.md D9.

    Note: packages that have no test files make "go test" print
    "no test files" while still exiting 0; that is expected, not a failure.

.NOTES
    Target runtime: PowerShell 7 (pwsh). The script only uses syntax that is
    also valid on Windows PowerShell 5.1, so it stays runnable on both.
#>

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repoRoot = Split-Path -Parent -Path $PSScriptRoot
$goModPath = Join-Path -Path $repoRoot -ChildPath 'go.mod'
if ([string]::IsNullOrWhiteSpace($repoRoot) -or -not (Test-Path -LiteralPath $goModPath)) {
    Write-Host 'LOCAL TESTS FAILED' -ForegroundColor Red
    Write-Host "Cannot locate repo root (go.mod) above: $PSScriptRoot" -ForegroundColor Red
    exit 1
}
Set-Location -LiteralPath $repoRoot

$packages = @(
    './internal/agent/...'
    './internal/workers/vhdx/...'
    './internal/workers/sysrestore/...'
    './internal/workers/homesku/...'
    './internal/protocol/...'
    './cmd/gui/...'
)

Write-Host '== back-EZ local path tests ==' -ForegroundColor Cyan
Write-Host "repo root : $repoRoot"
Write-Host ("packages  : {0}" -f ($packages -join ' '))
Write-Host 'note      : "no test files" is normal (exit code 0)'

$stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
$exitCode = 0
try {
    & go test -count=1 @packages
    $exitCode = $LASTEXITCODE
}
catch {
    $stopwatch.Stop()
    Write-Host 'LOCAL TESTS FAILED' -ForegroundColor Red
    Write-Host ("reason    : {0}" -f $_.Exception.Message) -ForegroundColor Red
    exit 1
}
$stopwatch.Stop()

$elapsed = [string]::Format(
    [System.Globalization.CultureInfo]::InvariantCulture,
    '{0:0.0}',
    $stopwatch.Elapsed.TotalSeconds
)

if ($exitCode -ne 0) {
    Write-Host ("LOCAL TESTS FAILED ({0}s) [go test exit code {1}]" -f $elapsed, $exitCode) -ForegroundColor Red
    exit 1
}

Write-Host ("LOCAL TESTS OK ({0}s)" -f $elapsed) -ForegroundColor Green
exit 0
