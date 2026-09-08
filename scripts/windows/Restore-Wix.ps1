#requires -Version 7.2
[CmdletBinding()]
param()
. "$PSScriptRoot/Common.ps1"
Assert-WindowsX64
$null = Get-Command dotnet -CommandType Application -ErrorAction Stop
$packaging = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../packaging/windows'))
# Explicit, local-tool restore only. No global install, credentials or SDK bootstrap.
Invoke-Checked 'dotnet' @('tool', 'restore', '--tool-manifest', "$packaging/.config/dotnet-tools.json",
    '--configfile', "$packaging/nuget.config", '--disable-parallel')
