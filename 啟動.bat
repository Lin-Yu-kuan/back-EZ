@echo off
chcp 65001 >nul
setlocal
title back-EZ 一鍵啟動精靈

:: 0. 先取得本腳本所在資料夾並切換過去。
::    提權後的 cmd 預設資料夾會變成 C:\Windows\System32，不能依賴相對路徑。
set "ROOT=%~dp0"
if not defined ROOT goto :no_root
cd /d "%ROOT%" 2>nul
if errorlevel 1 goto :no_root

echo ========================================================
echo         back-EZ 本機秒級還原系統 - 一鍵啟動精靈
echo ========================================================
echo.
echo 程式資料夾: %ROOT%
echo.

:: 1. 沒有管理員權限就帶「完整腳本路徑 + 工作資料夾」重新提權。
net session >nul 2>&1
if not errorlevel 1 goto :is_admin
echo [提示] 正在請求系統管理員權限以管理還原點與開機指標...
powershell -NoProfile -ExecutionPolicy Bypass -Command "Start-Process -FilePath '%~f0' -WorkingDirectory '%~dp0' -Verb RunAs"
if errorlevel 1 (
    echo [錯誤] 提權失敗，請對 啟動.bat 按右鍵選「以系統管理員身分執行」。
    pause
)
exit /b

:is_admin
:: 保險：提權後再切一次資料夾。
cd /d "%ROOT%" 2>nul

:: 2. back-ez.exe 不存在，或原始碼比它新，就自動重建。
if not exist "%ROOT%back-ez.exe" goto :build
powershell -NoProfile -ExecutionPolicy Bypass -Command "$exe=(Get-Item '%ROOT%back-ez.exe').LastWriteTimeUtc; $newer=Get-ChildItem -Path '%ROOT%cmd','%ROOT%internal' -Recurse -Filter *.go -File -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTimeUtc -gt $exe } | Select-Object -First 1; if ($newer) { exit 1 } else { exit 0 }"
if errorlevel 1 goto :build
goto :menu

:build
echo [編譯] 偵測到原始碼比 back-ez.exe 新（或尚未建置），正在重新編譯...
where go >nul 2>&1
if errorlevel 1 goto :no_go
go build -ldflags "-H windowsgui" -o "%ROOT%back-ez.exe" ./cmd/gui
if errorlevel 1 goto :build_failed
echo [成功] back-ez.exe 建置完成！
echo.
goto :menu

:no_go
echo [錯誤] 找不到 Go 編譯器，請先確認已安裝 Go 環境。
pause
exit /b 1

:build_failed
echo [錯誤] 編譯失敗，請檢查上方錯誤訊息。
pause
exit /b 1

:menu
echo 請選擇啟動模式：
echo   [1] 預覽模式（DryRun，預設）：只產生執行計畫，不會修改磁碟或 BCD
echo   [2] 實際模式：可能修改磁碟與開機設定，需再次輸入 YES 確認
echo.
set "MODE="
set /p "MODE=請輸入選項 [1/2]（直接按 Enter 為 1）: "
if not defined MODE set "MODE=1"
if "%MODE%"=="1" goto :preview
if "%MODE%"=="2" goto :real
echo [提示] 無法辨識選項 "%MODE%"，改用預設預覽模式。
goto :preview

:preview
echo [啟動] 正在啟動 back-EZ 本機控制台（預覽模式 DryRun）...
call :stop_old
start "" "%ROOT%back-ez.exe"
goto :done

:real
echo.
echo [警告] 實際模式可能修改磁碟與開機設定；目前僅在能力檢測全部通過且已授權時才會生效。
set "CONFIRM="
set /p "CONFIRM=若確定要進入實際模式，請輸入 YES："
if /I not "%CONFIRM%"=="YES" goto :cancel
echo [啟動] 正在啟動 back-EZ 本機控制台（實際模式）...
call :stop_old
start "" "%ROOT%back-ez.exe" --dryrun=false --i-understand-risk
goto :done

:cancel
echo [取消] 未輸入 YES，改用預覽模式啟動。
call :stop_old
start "" "%ROOT%back-ez.exe"
goto :done

:stop_old
taskkill /IM back-ez.exe /F >nul 2>&1
ping -n 2 127.0.0.1 >nul
exit /b

:done
echo.
echo [完成] 控制面板已開啟，您可以直接在桌面視窗上管理還原點。
timeout /t 3 >nul
exit /b

:no_root
echo [錯誤] 無法取得 啟動.bat 所在資料夾（%~dp0）。
pause
exit /b 1
