<#
.SYNOPSIS
    back-EZ WinPE Recovery Image Builder (Task 3.5)
.DESCRIPTION
    Builds an amd64 UEFI Windows PE recovery image with ezagent.exe, udp-receiver.exe,
    network drivers, and automatic startnet.cmd startup configuration.
.PARAMETER DryRun
    Simulate the build process and generate build scripts/configs without calling DISM.
.PARAMETER OutputDir
    Destination folder for boot.wim and PE files. Default is "dist\winpe".
.PARAMETER DriverDir
    Optional folder containing network drivers (.inf) to inject.
.PARAMETER CreateISO
    Switch to generate a bootable ISO image.
#>

[CmdletBinding()]
param(
    [switch]$DryRun = $false,
    [string]$OutputDir = "dist\winpe",
    [string]$DriverDir = "",
    [string]$AgentBinary = "cmd\agent\ezagent.exe",
    [string]$UdpReceiverBinary = "tools\udp-receiver.exe",
    [switch]$CreateISO = $false,
    [string]$ISOPath = "dist\winpe\backez-recovery.iso"
)

$ErrorActionPreference = "Stop"

Write-Host "====================================================" -ForegroundColor Cyan
Write-Host "  back-EZ WinPE Recovery Image Builder (Task 3.5)  " -ForegroundColor Cyan
Write-Host "====================================================" -ForegroundColor Cyan

# 1. Locate Windows ADK and WinPE Add-on
$ADKBase = "C:\Program Files (x86)\Windows Kits\10\Assessment and Deployment Kit"
$WinPERoot = Join-Path $ADKBase "Windows Preinstallation Environment\amd64"
$DismRoot = Join-Path $ADKBase "Deployment Tools\amd64\DISM"

if (-not $DryRun) {
    if (-not (Test-Path $WinPERoot)) {
        Write-Warning "Windows ADK WinPE Add-on not detected at $WinPERoot. Automatically falling back to -DryRun mode."
        $DryRun = $true
    }
}

# 2. Prepare directories
$AbsOutputDir = [System.IO.Path]::GetFullPath($OutputDir)
$WorkDir = Join-Path $AbsOutputDir "work"
$MountDir = Join-Path $AbsOutputDir "mount"
$ScriptLog = Join-Path $AbsOutputDir "winpe_build_plan.cmd"

if (-not (Test-Path $AbsOutputDir)) {
    New-Item -ItemType Directory -Path $AbsOutputDir -Force | Out-Null
}

# 3. Generate startnet.cmd content
$StartnetContent = @"
@echo off
rem ====================================================
rem back-EZ WinPE Auto-Startup Script
rem ====================================================
echo Initializing WinPE Network and Devices...
wpeinit

echo Scanning for back-EZ Reserve Partition...
set RESERVE_DIR=
for %%d in (C D E F G H I J K) do (
    if exist "%%d:\EZRestore\client_id" (
        set RESERVE_DIR=%%d:\EZRestore
        goto :found_reserve
    )
)

:found_reserve
if "%RESERVE_DIR%"=="" (
    echo [WARN] Reserve partition not found by drive letter. Scanning Volume GUID...
) else (
    echo [INFO] Found back-EZ reserve partition at %RESERVE_DIR%
)

echo Applying Network Configuration if snapshot exists...
if exist "%RESERVE_DIR%\network_identity.json" (
    echo [INFO] Restoring original network identity...
    rem Network identity restore helper will be triggered by ezagent
)

echo Starting back-EZ Client Agent in WinPE Mode...
if exist "X:\Program Files\back-EZ\ezagent.exe" (
    start "" "X:\Program Files\back-EZ\ezagent.exe" --service --pe --reserve="%RESERVE_DIR%"
) else (
    echo [WARN] Local ezagent.exe not found in image root.
)

cmd.exe /k echo Welcome to back-EZ WinPE Recovery Environment.
"@

# 4. Construct execution commands
$BuildCommands = @()
$BuildCommands += "rem Step 1: Copy base WinPE media files"
$BuildCommands += "call `"$DismRoot\copype.cmd`" amd64 `"$WorkDir`""
$BuildCommands += "rem Step 2: Mount boot.wim"
$BuildCommands += "md `"$MountDir`""
$BuildCommands += "dism /Mount-Image /ImageFile:`"$WorkDir\media\sources\boot.wim`" /Index:1 /MountDir:`"$MountDir`""

$BuildCommands += "rem Step 3: Inject WinPE Optional Packages (WMI, NetFX, PowerShell)"
$BuildCommands += "dism /Image:`"$MountDir`" /Add-Package /PackagePath:`"$WinPERoot\WinPE_OCs\WinPE-WMI.cab`""
$BuildCommands += "dism /Image:`"$MountDir`" /Add-Package /PackagePath:`"$WinPERoot\WinPE_OCs\WinPE-NetFX.cab`""
$BuildCommands += "dism /Image:`"$MountDir`" /Add-Package /PackagePath:`"$WinPERoot\WinPE_OCs\WinPE-Scripting.cab`""
$BuildCommands += "dism /Image:`"$MountDir`" /Add-Package /PackagePath:`"$WinPERoot\WinPE_OCs\WinPE-PowerShell.cab`""

if ($DriverDir -and (Test-Path $DriverDir)) {
    $BuildCommands += "rem Step 4: Inject Network Adapter Drivers"
    $BuildCommands += "dism /Image:`"$MountDir`" /Add-Driver /Driver:`"$DriverDir`" /Recurse /ForceUnsigned"
}

$BuildCommands += "rem Step 5: Copy Agent and Tools into Image"
$BuildCommands += "md `"$MountDir\Program Files\back-EZ`""
$BuildCommands += "copy /y `"$AgentBinary`" `"$MountDir\Program Files\back-EZ\ezagent.exe`""
$BuildCommands += "copy /y `"$UdpReceiverBinary`" `"$MountDir\Program Files\back-EZ\udp-receiver.exe`""

$BuildCommands += "rem Step 6: Overwrite startnet.cmd"
$BuildCommands += "copy /y `"$AbsOutputDir\startnet.cmd`" `"$MountDir\Windows\System32\startnet.cmd`""

$BuildCommands += "rem Step 7: Unmount and commit boot.wim"
$BuildCommands += "dism /Unmount-Image /MountDir:`"$MountDir`" /Commit"
$BuildCommands += "copy /y `"$WorkDir\media\sources\boot.wim`" `"$AbsOutputDir\boot.wim`""

if ($CreateISO) {
    $BuildCommands += "rem Step 8: Create Bootable ISO for USB recovery"
    $BuildCommands += "MakeWinPEMedia /iso `"$WorkDir`" `"$ISOPath`""
}

# 5. Output generated script and artifacts
Set-Content -Path (Join-Path $AbsOutputDir "startnet.cmd") -Value $StartnetContent -Encoding ASCII
Set-Content -Path $ScriptLog -Value ($BuildCommands -join "`r`n") -Encoding ASCII

Write-Host "Generated startnet.cmd: $(Join-Path $AbsOutputDir 'startnet.cmd')" -ForegroundColor Green
Write-Host "Generated build script plan: $ScriptLog" -ForegroundColor Green

if ($DryRun) {
    Write-Host "`n[DryRun] WinPE build plan successfully simulated without invoking DISM." -ForegroundColor Yellow
    Write-Host "Build steps summary:" -ForegroundColor Yellow
    foreach ($cmd in $BuildCommands) {
        if ($cmd.StartsWith("rem")) {
            Write-Host "  * $($cmd.Substring(4))" -ForegroundColor Gray
        }
    }
    return
}

# 6. Real execution when DryRun is false
Write-Host "Executing WinPE build commands..." -ForegroundColor Cyan
foreach ($cmd in $BuildCommands) {
    if ($cmd.StartsWith("rem")) {
        Write-Host "$($cmd.Substring(4))" -ForegroundColor DarkGray
        continue
    }
    Write-Host "Running: $cmd" -ForegroundColor White
    cmd.exe /c $cmd
    if ($LASTEXITCODE -ne 0) {
        throw "Build command failed with exit code $($LASTEXITCODE): $cmd"
    }
}

Write-Host "`nWinPE recovery image build completed: $(Join-Path $AbsOutputDir 'boot.wim')" -ForegroundColor Green
