@echo off
REM ============================================
REM  CodeBuddy Pool 一键启动 (Go 代理 + 管理面板)
REM ============================================

REM --- 1. 启动 Go 代理 ---
echo [1/2] Starting Go proxy...
cd /d %~dp0go-proxy-cto
start "cto-proxy" /min cto-proxy.exe

REM --- 2. 启动管理面板 ---
echo [2/2] Starting dashboard...
cd /d %~dp0dashboard
REM 使用系统 PATH 中的 python（需先 pip install -r requirements.txt）
start "dashboard" /min python server.py

echo.
echo   +============================================+
echo   ^|  Proxy:     http://127.0.0.1:9091           ^|
echo   ^|  Dashboard: http://localhost:9100            ^|
echo   +============================================+
echo.
timeout /t 3 >nul
curl -s http://127.0.0.1:9091/stats
echo.
