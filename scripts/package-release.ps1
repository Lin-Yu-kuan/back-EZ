<#
.SYNOPSIS
    back-EZ 一鍵打包發布腳本 (Standalone Desktop Release)
#>

$ErrorActionPreference = "Stop"

Write-Host "========================================================" -ForegroundColor Cyan
Write-Host "       back-EZ 單機桌面整合包 打包腳本                 " -ForegroundColor Cyan
Write-Host "========================================================" -ForegroundColor Cyan

$distDir = Join-Path $PSScriptRoot "..\dist\standalone"
if (Test-Path $distDir) {
    Remove-Item -Path $distDir -Recurse -Force
}
New-Item -ItemType Directory -Path $distDir -Force | Out-Null

Write-Host "1. 編譯 back-ez.exe (GUI 單機主程式)..." -ForegroundColor Yellow
go build -ldflags "-H windowsgui -s -w" -o "$distDir\back-ez.exe" .\cmd\gui

Write-Host "2. 編譯 ezagent.exe (背景服務/WinPE 執行檔)..." -ForegroundColor Yellow
go build -ldflags "-s -w" -o "$distDir\ezagent.exe" .\cmd\agent

Write-Host "3. 複製啟動腳本..." -ForegroundColor Yellow
Copy-Item ".\啟動.bat" "$distDir\"

Write-Host "`n[成功] 獨立綠色免安裝整合包已產出至 $distDir" -ForegroundColor Green
Write-Host "使用者只要點擊 dist\standalone\啟動.bat 即可一鍵運行！" -ForegroundColor Green

