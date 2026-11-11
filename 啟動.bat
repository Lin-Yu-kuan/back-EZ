@echo off
chcp 65001 >nul
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

:: 2. 檢查主程式是否存在，若無則自動編譯
if not exist "back-ez.exe" (
    echo [編譯] 首次啟動，正在編譯 back-ez.exe 整合應用程式...
    where go >nul 2>&1
    if %errorLevel% neq 0 (
        echo [錯誤] 找不到 Go 編譯器，請先確認已安裝 Go 環境。
        pause
        exit /b 1
    )
    go build -ldflags "-H windowsgui" -o back-ez.exe ./cmd/gui
    if %errorLevel% neq 0 (
        echo [錯誤] 編譯失敗，請檢查錯誤訊息。
        pause
        exit /b 1
    )
    echo [成功] back-ez.exe 編譯完成！
    echo.
)

:: 3. 啟動本機控制台 (真實執行模式)
echo [啟動] 正在啟動 back-EZ 本機控制台 (真實生效模式)...
start "" "back-ez.exe" --dryrun=false

echo [完成] 控制面板已開啟，您可以直接在桌面視窗上管理還原點。
timeout /t 3 >nul
exit /b

