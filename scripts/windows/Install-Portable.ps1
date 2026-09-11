#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $Destination,
    [ValidateSet('x64', 'arm64')][string] $Architecture
)
. "$PSScriptRoot/Common.ps1"
$platform = if ($Architecture) { Convert-WindowsArchitectureToPlatform $Architecture } else { Get-NativeWindowsPlatform }
Assert-NativeWindowsArchitecture $platform
$out = Get-NewDirectoryPath $Destination
Invoke-WindowsCtl @('portable', '--dir', $ReleaseDir, '--manifest', $Manifest, '--output', $out, '--platform', $platform)
Write-Host "Verified ZIP extracted under $out. Run the versioned folder's sodapop.exe."
Write-Host 'No PATH, shell profile, registry, account state, or credentials were modified.'
