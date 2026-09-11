#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $OutputDir,
    [string] $PackageIdentifier = 'VeVarunSharma.Sodapop',
    [ValidateSet('x64', 'arm64')][string] $Architecture,
    [switch] $ValidateWinGet,
    [switch] $InstallWinGet,
    [switch] $TestScoop
)
. "$PSScriptRoot/Common.ps1"
$platform = if ($Architecture) { Convert-WindowsArchitectureToPlatform $Architecture } else { Get-NativeWindowsPlatform }
$Architecture = Convert-WindowsPlatformToArchitecture $platform
Assert-NativeWindowsArchitecture $platform
if (-not ($ValidateWinGet -or $InstallWinGet -or $TestScoop)) { throw 'Select a channel test explicitly.' }
if ($InstallWinGet -or $TestScoop) { Assert-DisposableRunner $platform }
if ($ValidateWinGet -or $InstallWinGet) { $null = Get-Command winget.exe -CommandType Application -ErrorAction Stop }
if ($TestScoop) { $scoop = Get-Command scoop -ErrorAction Stop }
$work = Get-NewDirectoryPath $OutputDir
$null = New-Item -ItemType Directory -Path $work
$generated = Join-Path $work 'generated'
Invoke-WindowsCtl @('manifests', '--dir', $ReleaseDir, '--manifest', $Manifest,
    '--output', $generated, '--package-id', $PackageIdentifier)
$current = Get-VerifiedRelease $ReleaseDir $Manifest $platform
$wingetDir = Join-Path $generated ('winget/manifests/' + $PackageIdentifier.Substring(0, 1).ToLowerInvariant() +
    '/' + $PackageIdentifier.Replace('.', '/') + '/' + $current.Release.version)
$results = [ordered]@{ manifest_validation = $false; winget_local_manifest_install = $false; scoop_local_manifest_install = $false }
if ($ValidateWinGet -or $InstallWinGet) {
    Invoke-Checked 'winget.exe' @('validate', '--manifest', $wingetDir, '--disable-interactivity')
    $results.manifest_validation = $true
}
if ($InstallWinGet) {
    # This script never enables LocalManifestFiles. The disposable runner owner
    # must provision that prerequisite separately; winget fails if it is disabled.
    $link = Join-Path $env:LOCALAPPDATA 'Microsoft/WinGet/Links/sodapop.exe'
    if (Test-Path -LiteralPath $link) { throw 'WinGet Sodapop alias already exists.' }
    Invoke-Checked 'winget.exe' @('install', '--manifest', $wingetDir, '--scope', 'user',
        '--accept-package-agreements', '--accept-source-agreements', '--disable-interactivity')
    $item = Get-Item -LiteralPath $link -Force
    if (-not ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) { throw 'Expected the WinGet portable command link.' }
    $target = $item.ResolveLinkTarget($true).FullName
    $packageRoot = [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'Microsoft/WinGet/Packages')) + [IO.Path]::DirectorySeparatorChar
    if (-not $target.StartsWith($packageRoot, [StringComparison]::OrdinalIgnoreCase)) {
        throw 'WinGet link points outside its per-user package directory.'
    }
    Assert-Hash $target $current.Artifact.binary_sha256
    & "$PSScriptRoot/Test-Native.ps1" -ReleaseDir $ReleaseDir -Manifest $Manifest `
        -OutputDir (Join-Path $work 'winget-native') -Architecture $Architecture `
        -InstalledBinary $target -InstalledCommand $link
    Invoke-Checked 'winget.exe' @('uninstall', '--id', $PackageIdentifier, '--exact', '--scope', 'user',
        '--disable-interactivity')
    if (Test-Path -LiteralPath $link) { throw 'WinGet uninstall left its portable alias.' }
    $results.winget_local_manifest_install = $true
}
if ($TestScoop) {
    $scoopRoot = if ($env:SCOOP) { [IO.Path]::GetFullPath($env:SCOOP) } else { Join-Path $env:USERPROFILE 'scoop' }
    $app = Join-Path $scoopRoot 'apps/sodapop'
    if (Test-Path -LiteralPath $app) { throw 'Scoop Sodapop already exists.' }
    $scoopManifest = Join-Path $generated 'scoop/bucket/sodapop.json'
    Invoke-Checked $scoop.Source @('install', $scoopManifest)
    $installed = Join-Path $app "$($current.Release.version)/sodapop.exe"
    Assert-Hash $installed $current.Artifact.binary_sha256
    $shim = Get-RegularFile (Join-Path $scoopRoot 'shims/sodapop.exe')
    if (-not $shim) { throw 'Scoop did not create its command shim.' }
    & "$PSScriptRoot/Test-Native.ps1" -ReleaseDir $ReleaseDir -Manifest $Manifest `
        -OutputDir (Join-Path $work 'scoop-native') -Architecture $Architecture `
        -InstalledBinary $installed -InstalledCommand $shim
    Invoke-Checked $scoop.Source @('uninstall', 'sodapop')
    if (Test-Path -LiteralPath (Join-Path $scoopRoot 'shims/sodapop.exe')) { throw 'Scoop uninstall left its command shim.' }
    $results.scoop_local_manifest_install = $true
}
$results.public_registry_availability = 'not-tested'
$results.community_acceptance = 'not-established'
Write-NewJson (Join-Path $work 'channel-evidence.json') $results
