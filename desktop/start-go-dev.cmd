@echo off
setlocal
set ELECTRON_RUN_AS_NODE=
set ELECTRON_NO_ATTACH_CONSOLE=1
start "Astrorder Go Dev" "%~dp0node_modules\electron\dist\electron.exe" "%~dp0dev-main.js"
