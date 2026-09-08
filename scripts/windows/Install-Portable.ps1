#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $Destination
)
. "$PSScriptRoot/Common.ps1"
Assert-WindowsX64
$out = Get-NewDirectoryPath $Destination
Invoke-WindowsCtl @('portable', '--dir', $ReleaseDir, '--manifest', $Manifest, '--output', $out)
Write-Host "Verified ZIP extracted under $out. Run the versioned folder's sodapop.exe."
Write-Host 'No PATH, shell profile, registry, account state, or credentials were modified.'
