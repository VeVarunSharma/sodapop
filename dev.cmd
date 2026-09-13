@echo off
setlocal
set "SODAPOP_DEV_ROOT=%~dp0"
powershell.exe -NoLogo -NoExit -ExecutionPolicy Bypass -Command ^
  "Set-Location -LiteralPath $env:SODAPOP_DEV_ROOT; Set-Alias -Name make -Value (Join-Path $env:SODAPOP_DEV_ROOT 'make.cmd'); Write-Host 'Sodapop development shell ready. Run make build or make start.'"
