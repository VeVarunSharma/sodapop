#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $OutputDir,
    [switch] $UnsignedCandidate,
    [switch] $WixEulaAccepted
)
. "$PSScriptRoot/Common.ps1"
Assert-WindowsX64
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
    Invoke-WindowsCtl @('prepare-msi', '--dir', $releasePath, '--manifest', $manifestPath, '--output', $out)
    $inputPath = Join-Path $out 'msi-input.json'
    $inputMetadata = Get-Content -LiteralPath $inputPath -Raw | ConvertFrom-Json
    Assert-StagedPayload $out $inputMetadata
    $msi = Join-Path $out "sodapop-$($inputMetadata.version)-windows-amd64-unsigned.msi"
    Invoke-Checked 'dotnet' @('tool', 'run', 'wix', 'build', '-acceptEula', 'wix7',
        '-arch', 'x64', '-d', "Version=$($inputMetadata.version)", '-d', "ProductCode=$($inputMetadata.product_code)",
        '-d', "PayloadDir=$(Join-Path $out 'payload')", '-intermediateFolder', (Join-Path $out 'wixobj'),
        '-pdbtype', 'none', '-o', $msi, (Join-Path $packaging 'Sodapop.wxs'), (Join-Path $out 'Payload.wxs'))
    Assert-StagedPayload $out $inputMetadata
    & "$PSScriptRoot/Complete-Msi.ps1" -Msi $msi -InputMetadata $inputPath `
        -Output (Join-Path $out 'msi-candidate.json') -UnsignedCandidate
    Write-Host "Unsigned candidate: $msi"
} finally {
    Pop-Location
}
