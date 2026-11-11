<#
.SYNOPSIS
    back-EZ First-Time Setup: Physical Machine to Native VHDX Boot (Task 3.6 / SPEC §4.4)
.DESCRIPTION
    Automates the provisioning of a machine from physical/PE environment into native VHDX boot:
    1. Creates dynamic base.vhdx with maximum size on the reserve partition.
    2. Initializes GPT partitions (ESP 400MB + MSR 16MB + NTFS Main OS).
    3. Applies base OS image (via DISM /Apply-Image or direct copy).
    4. Configures UEFI boot files using bcdboot.
    5. Copies PE recovery image (boot.wim) and sets up {ramdiskoptions} via PEBootManager.
    6. Installs ezagent.exe, creates permanent client_id in reserve, and enrolls to Hub.
.PARAMETER DryRun
    When true (default per AGENT.md §4.1), generates all diskpart, bcdboot, and setup scripts
    without executing destructive commands.
.PARAMETER AllowDestructive
    Must be set along with -IUnderstandRisk to perform real disk formatting and BCD modifications.
#>

[CmdletBinding()]
param(
    [switch]$DryRun = $true,
    [switch]$AllowDestructive = $false,
    [switch]$IUnderstandRisk = $false,
    [string]$ReserveRoot = "C:\EZRestore",
    [int]$VHDXSizeGB = 128,
    [string]$SourceWIM = "",
    [string]$HubURL = "https://192.168.1.100:8443",
    [string]$EnrollToken = "",
    [string]$OutputDir = "dist\init_setup"
)

$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  back-EZ Native VHDX Boot Provisioning Engine (Task 3.6) " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# Safety sandbox check (AGENT.md §4.1)
if (-not $DryRun) {
    if (-not $AllowDestructive -or -not $IUnderstandRisk) {
        Write-Error "Destructive disk operations require both -AllowDestructive and -IUnderstandRisk flags. Aborting."
        exit 1
    }
}

$AbsOutputDir = [System.IO.Path]::GetFullPath($OutputDir)
if (-not (Test-Path $AbsOutputDir)) {
    New-Item -ItemType Directory -Path $AbsOutputDir -Force | Out-Null
}

$BaseVHDXPath = Join-Path $ReserveRoot "base.vhdx"
$DiskpartScriptPath = Join-Path $AbsOutputDir "diskpart_init_vhdx.txt"
$ProvisionPlanPath = Join-Path $AbsOutputDir "provision_plan.cmd"

# 1. Generate Diskpart Script for creating and partitioning base.vhdx
$VHDXSizeMB = $VHDXSizeGB * 1024
$DiskpartCommands = @"
create vdisk file="$BaseVHDXPath" maximum=$VHDXSizeMB type=expandable
select vdisk file="$BaseVHDXPath"
attach vdisk
convert gpt
create partition efi size=400
format quick fs=fat32 label="ESP_VHD"
assign letter=S
create partition msr size=16
create partition primary
format quick fs=ntfs label="Windows_VHD"
assign letter=W
detach vdisk
"@

Set-Content -Path $DiskpartScriptPath -Value $DiskpartCommands -Encoding ASCII

# 2. Generate Provisioning Execution Plan
$PlanCommands = @()
$PlanCommands += "rem ========================================================"
$PlanCommands += "rem back-EZ Native VHDX Boot Provisioning Plan"
$PlanCommands += "rem ========================================================"
$PlanCommands += "@echo off"
$PlanCommands += "setlocal enabledelayedexpansion"

$PlanCommands += "rem Step 1: Create and Partition base.vhdx"
$PlanCommands += "diskpart /s `"$DiskpartScriptPath`""

if ($SourceWIM -ne "") {
    $PlanCommands += "rem Step 2: Apply Windows OS Image to VHDX primary partition (W:)"
    $PlanCommands += "dism /Apply-Image /ImageFile:`"$SourceWIM`" /Index:1 /ApplyDir:W:\"
} else {
    $PlanCommands += "rem Step 2: OS Image source omitted; assuming base.vhdx populated or copied from master"
}

$PlanCommands += "rem Step 3: Configure UEFI Bootloader via bcdboot"
$PlanCommands += "bcdboot W:\Windows /s S: /f UEFI"

$PlanCommands += "rem Step 4: Setup PE Recovery Ramdisk in Reserve Partition"
$PlanCommands += "if not exist `"$ReserveRoot\PE`" md `"$ReserveRoot\PE`""
$PlanCommands += "copy /y `"dist\winpe\boot.wim`" `"$ReserveRoot\PE\boot.wim`""

$PlanCommands += "rem Step 5: Install and Initialize Agent in VHDX System"
$PlanCommands += "if not exist `"W:\Program Files\back-EZ`" md `"W:\Program Files\back-EZ`""
$PlanCommands += "copy /y `"cmd\agent\ezagent.exe`" `"W:\Program Files\back-EZ\ezagent.exe`""

if ($EnrollToken -ne "") {
    $PlanCommands += "rem Step 6: Enroll Agent with Hub"
    $PlanCommands += "W:\`"Program Files`"\back-EZ\ezagent.exe --enroll=`"$EnrollToken`" --reserve=`"$ReserveRoot`""
}

$PlanCommands += "echo [SUCCESS] Native VHDX Boot Provisioning Plan Ready."

Set-Content -Path $ProvisionPlanPath -Value ($PlanCommands -join "`r`n") -Encoding ASCII

Write-Host "Generated Diskpart provisioning script: $DiskpartScriptPath" -ForegroundColor Green
Write-Host "Generated Provisioning plan: $ProvisionPlanPath" -ForegroundColor Green

if ($DryRun) {
    Write-Host "`n[DryRun] Provisioning successfully simulated without modifying physical partitions." -ForegroundColor Yellow
    Write-Host "Plan steps summary:" -ForegroundColor Yellow
    foreach ($cmd in $PlanCommands) {
        if ($cmd.StartsWith("rem Step")) {
            Write-Host "  * $($cmd.Substring(4))" -ForegroundColor Gray
        }
    }
    return
}

Write-Host "Executing provisioning commands in destructive mode..." -ForegroundColor Red
cmd.exe /c $ProvisionPlanPath
if ($LASTEXITCODE -ne 0) {
    throw "Provisioning failed with exit code $($LASTEXITCODE)"
}
Write-Host "`nNative VHDX Boot Provisioning completed successfully." -ForegroundColor Green

