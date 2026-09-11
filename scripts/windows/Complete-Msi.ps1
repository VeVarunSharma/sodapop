#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $Msi,
    [Parameter(Mandatory)][string] $InputMetadata,
    [Parameter(Mandatory)][string] $Output,
    [string] $SignerThumbprint,
    [switch] $UnsignedCandidate
)
. "$PSScriptRoot/Common.ps1"
$msiPath = Get-RegularFile $Msi
$inputPath = Get-RegularFile $InputMetadata
$outputPath = Get-NewDirectoryPath $Output
if ([IO.Path]::GetExtension($msiPath) -ine '.msi') { throw 'Expected an MSI file.' }
$inputData = Get-Content -LiteralPath $inputPath -Raw | ConvertFrom-Json
if ($inputData.platform -notin @('windows/amd64', 'windows/arm64') -or
    $inputData.architecture -cne (Convert-WindowsPlatformToArchitecture $inputData.platform) -or
    $inputData.wix_architecture -cne $inputData.architecture -or
    $inputData.archive -cne "sodapop-$($inputData.version)-$($inputData.platform.Replace('/', '-')).zip") {
    throw 'MSI input has invalid or inconsistent architecture metadata.'
}
Assert-NativeWindowsArchitecture $inputData.platform
Assert-StagedPayload ([IO.Path]::GetDirectoryName($inputPath)) $inputData
$expectedMsi = "sodapop-$($inputData.version)-$($inputData.platform.Replace('/', '-'))" +
    $(if ($UnsignedCandidate) { '-unsigned.msi' } else { '.msi' })
if ([IO.Path]::GetFileName($msiPath) -cne $expectedMsi) {
    throw "MSI filename must be $expectedMsi"
}
$signature = Get-AuthenticodeSignature -LiteralPath $msiPath
$status = 'unsigned-candidate'
$thumbprint = $null
if ($UnsignedCandidate) {
    if ($SignerThumbprint -or $signature.Status -ne 'NotSigned') {
        throw 'Unsigned candidate mode requires an actually unsigned MSI and no signer.'
    }
} else {
    if ($SignerThumbprint -notmatch '^[a-fA-F0-9]{40}$' -or $signature.Status -ne 'Valid' -or
        $null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ine $SignerThumbprint) {
        throw 'Signed MSI requires valid Authenticode and the explicitly expected signer thumbprint.'
    }
    $status = 'authenticode-verified'
    $thumbprint = $signature.SignerCertificate.Thumbprint
}
# Hash the final container independently, after signing; ZIP hashes never describe an MSI.
Write-NewJson $outputPath ([ordered]@{
    schema_version = 1
    version = $inputData.version
    platform = $inputData.platform
    architecture = $inputData.architecture
    wix_architecture = $inputData.wix_architecture
    scope = 'perUser'
    product_code = $inputData.product_code
    upgrade_code = $inputData.upgrade_code
    msi = [IO.Path]::GetFileName($msiPath)
    msi_sha256 = (Get-FileHash -LiteralPath $msiPath -Algorithm SHA256).Hash.ToLowerInvariant()
    signing_state = $status
    signer_thumbprint = $thumbprint
    source_archive = $inputData.archive
    source_archive_sha256 = $inputData.archive_sha256
    binary_sha256 = $inputData.binary_sha256
})
