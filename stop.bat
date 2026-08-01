@echo off
REM ============================================
REM  CodeBuddy Pool 一键停止 (Go 代理 + 管理面板)
REM ============================================

echo [1/1] Stopping Go proxy and dashboard...
powershell -NoProfile -Command "Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'cto-proxy.exe' -or ($_.ExecutablePath -match 'codebuddy' -and ($_.CommandLine -match 'server\.py' -or $_.CommandLine -match 'spawn_main')) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"

echo.
echo All stopped.
pause
