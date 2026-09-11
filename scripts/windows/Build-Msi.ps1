#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $OutputDir,
    [ValidateSet('x64', 'arm64')][string] $Architecture,
    [switch] $UnsignedCandidate,
    [switch] $WixEulaAccepted
)
. "$PSScriptRoot/Common.ps1"
$platform = if ($Architecture) { Convert-WindowsArchitectureToPlatform $Architecture } else { Get-NativeWindowsPlatform }
$Architecture = Convert-WindowsPlatformToArchitecture $platform
Assert-NativeWindowsArchitecture $platform
if (-not $UnsignedCandidate) {
    throw 'Building produces an unsigned MSI. Explicitly select -UnsignedCandidate; signing is a separate owner-controlled step.'
}
if (-not $WixEulaAccepted) {
    throw 'WiX 7 requires owner review of its EULA/maintenance terms. Supply -WixEulaAccepted only after that review.'
}
$out = Get-NewDirectoryPath $OutputDir
$releasePath = (Get-Item -LiteralPath $ReleaseDir).FullName
$manifestPath = Get-RegularFile $Manifest
$packaging = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../packaging/windows'))
$null = Get-Command dotnet -CommandType Application -ErrorAction Stop
Push-Location $packaging
try {
    $version = Invoke-Checked 'dotnet' @('tool', 'run', 'wix', '--version')
    if (($version -join "`n").Trim() -notmatch '^7\.0\.0(\+[^ \r\n]+)?$') {
        throw 'Expected pinned local WiX 7.0.0; run scripts/windows/Restore-Wix.ps1 explicitly.'
    }
    Invoke-WindowsCtl @('prepare-msi', '--dir', $releasePath, '--manifest', $manifestPath, '--output', $out,
        '--platform', $platform)
    $inputPath = Join-Path $out 'msi-input.json'
    $inputMetadata = Get-Content -LiteralPath $inputPath -Raw | ConvertFrom-Json
    Assert-StagedPayload $out $inputMetadata
    Assert-WindowsExecutableArchitecture (Join-Path $out 'payload/sodapop.exe') $platform
    if ($inputMetadata.platform -cne $platform -or $inputMetadata.architecture -cne $Architecture -or
        $inputMetadata.wix_architecture -cne $Architecture) {
        throw 'MSI input architecture differs from the selected native architecture.'
    }
    $productWxs = Join-Path $out 'Product.wxs'
    $productSource = Get-Content -LiteralPath (Join-Path $packaging 'Sodapop.wxs') -Raw
    if ([regex]::Matches($productSource, '<MajorUpgrade ').Count -ne 1 -or
        $productSource.Contains('AllowSameVersionUpgrades=')) {
        throw 'Unexpected base WiX major-upgrade authoring.'
    }
    $productSource = $productSource.Replace('<MajorUpgrade ',
        '<MajorUpgrade AllowSameVersionUpgrades="yes" ')
    $stream = [IO.File]::Open($productWxs, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes($productSource)
        $stream.Write($bytes, 0, $bytes.Length)
    } finally {
        $stream.Dispose()
    }
    $msi = Join-Path $out "sodapop-$($inputMetadata.version)-$($platform.Replace('/', '-'))-unsigned.msi"
    Invoke-Checked 'dotnet' @('tool', 'run', 'wix', 'build', '-acceptEula', 'wix7',
        '-arch', $inputMetadata.wix_architecture, '-d', "Version=$($inputMetadata.version)", '-d', "ProductCode=$($inputMetadata.product_code)",
        '-d', "PayloadDir=$(Join-Path $out 'payload')", '-intermediateFolder', (Join-Path $out 'wixobj'),
        '-pdbtype', 'none', '-o', $msi, $productWxs, (Join-Path $out 'Payload.wxs'))
    Assert-StagedPayload $out $inputMetadata
    & "$PSScriptRoot/Complete-Msi.ps1" -Msi $msi -InputMetadata $inputPath `
        -Output (Join-Path $out 'msi-candidate.json') -UnsignedCandidate
    Write-Host "Unsigned candidate: $msi"
} finally {
    Pop-Location
}
