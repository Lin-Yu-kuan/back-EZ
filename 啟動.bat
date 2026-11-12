@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion
title back-EZ 一鍵啟動精靈

echo ========================================================
echo         back-EZ 本機秒級還原系統 - 一鍵啟動精靈
echo ========================================================
echo.

:: 1. 檢查是否具有系統管理員身分
net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [提示] 正在請求系統管理員權限以管理還原點與開機指標...
    powershell -NoProfile -Command "Start-Process '%~0' -Verb RunAs"
    exit /b
)

cd /d "%~dp0"

:: 2. 若 back-ez.exe 不存在，或 cmd/、internal/ 下有 .go 比它新，則自動重新建置
set "NEED_BUILD=0"
if not exist "back-ez.exe" (
    set "NEED_BUILD=1"
) else (
    powershell -NoProfile -Command "$exe=(Get-Item 'back-ez.exe').LastWriteTimeUtc; if (Get-ChildItem -Path 'cmd','internal' -Recurse -Filter *.go -File | Where-Object { $_.LastWriteTimeUtc -gt $exe } | Select-Object -First 1) { exit 1 } else { exit 0 }"
    if errorlevel 1 set "NEED_BUILD=1"
)

if "!NEED_BUILD!"=="1" (
    echo [編譯] 偵測到 Go 原始碼比 back-ez.exe 新（或尚未建置），正在重新編譯...
    where go >nul 2>&1
    if errorlevel 1 (
        echo [錯誤] 找不到 Go 編譯器，請先確認已安裝 Go 環境。
        pause
        exit /b 1
    )
    go build -ldflags "-H windowsgui" -o back-ez.exe ./cmd/gui
    if errorlevel 1 (
        echo [錯誤] 編譯失敗，請檢查上方錯誤訊息。
        pause
        exit /b 1
    )
    echo [成功] back-ez.exe 建置完成！
    echo.
)

:: 3. 選擇啟動模式（預設為預覽模式 DryRun）
echo 請選擇啟動模式：
echo   [1] 預覽模式（DryRun，預設）：只產生執行計畫，不會修改磁碟或 BCD
echo   [2] 實際模式：可能修改磁碟與開機設定，需再次輸入 YES 確認
echo.
set "MODE="
set /p "MODE=請輸入選項 [1/2]（直接按 Enter 為 1）: "
if not defined MODE set "MODE=1"

if "!MODE!"=="1" (
    echo [啟動] 正在啟動 back-EZ 本機控制台（預覽模式 DryRun）...
    start "" "back-ez.exe"
) else if "!MODE!"=="2" (
    echo.
    echo [警告] 實際模式可能修改磁碟與開機設定；目前僅在能力檢測全部通過且已授權時才會生效。
    set "CONFIRM="
    set /p "CONFIRM=若確定要進入實際模式，請輸入 YES："
    if /I not "!CONFIRM!"=="YES" (
        echo [取消] 未輸入 YES，改用預覽模式啟動。
        start "" "back-ez.exe"
    ) else (
        echo [啟動] 正在啟動 back-EZ 本機控制台（實際模式）...
        start "" "back-ez.exe" --dryrun=false --i-understand-risk
    )
) else (
    echo [提示] 無法辨識選項 "!MODE!"，改用預設預覽模式啟動。
    start "" "back-ez.exe"
)

echo.
echo [完成] 控制面板已開啟，您可以直接在桌面視窗上管理還原點。
timeout /t 3 >nul
exit /b
