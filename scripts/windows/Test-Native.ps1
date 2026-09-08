#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string] $ReleaseDir,
    [Parameter(Mandatory)][string] $Manifest,
    [Parameter(Mandatory)][string] $OutputDir,
    [string] $InstalledBinary,
    [string] $InstalledCommand,
    [switch] $PublicDownload,
    [switch] $MsiLifecycle,
    [string] $Msi,
    [string] $MsiMetadata,
    [string] $PreviousReleaseDir,
    [string] $PreviousManifest,
    [string] $PreviousMsi,
    [string] $PreviousMsiMetadata,
    [switch] $AllowUnsignedCandidate
)
. "$PSScriptRoot/Common.ps1"
Assert-WindowsX64
if ($InstalledCommand -and -not $InstalledBinary) { throw '-InstalledCommand requires an independently hash-checked -InstalledBinary.' }
if ($MsiLifecycle) { Assert-DisposableRunner }
$work = Get-NewDirectoryPath $OutputDir
$null = New-Item -ItemType Directory -Path $work
$manifestPath = Get-RegularFile $Manifest
if ($PublicDownload) {
    # The caller supplies the trusted final manifest. Public bytes alone are not
    # publisher identity; attestations/signing remain the parent release gate.
    $downloadRelease = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($downloadRelease.version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$') {
        throw 'Invalid public version.'
    }
    $asset = @($downloadRelease.artifacts | Where-Object platform -CEQ 'windows/amd64')
    if ($asset.Count -ne 1 -or $asset[0].archive -cne "sodapop-$($downloadRelease.version)-windows-amd64.zip") {
        throw 'Invalid public Windows archive.'
    }
    $ReleaseDir = Join-Path $work 'download'
    $null = New-Item -ItemType Directory -Path $ReleaseDir
    $zip = Join-Path $ReleaseDir $asset[0].archive
    $url = "https://github.com/VeVarunSharma/sodapop/releases/download/v$($downloadRelease.version)/$($asset[0].archive)"
    Invoke-WebRequest -Uri $url -OutFile $zip -MaximumRedirection 5
    Assert-Hash $zip $asset[0].archive_sha256
    Invoke-WebRequest -Uri "$url.sha256" -OutFile "$zip.sha256" -MaximumRedirection 5
}
$current = Get-VerifiedRelease $ReleaseDir $manifestPath
$evidence = [Collections.Generic.List[object]]::new()
$profile = Join-Path $work 'isolated-profile'
foreach ($child in @('', 'AppData/Roaming', 'AppData/Local', 'Temp', 'project')) {
    $null = New-Item -ItemType Directory -Path (Join-Path $profile $child) -Force
}

function Test-CommandBytes([string] $Binary, $ReleaseInfo, [string] $Phase, [string] $LaunchPath = '') {
    Assert-Hash $Binary $ReleaseInfo.Artifact.binary_sha256
    if (-not $LaunchPath) { $LaunchPath = $Binary }
    foreach ($argument in @('--help', '--version', '--check-runtime', '--check-runtime')) {
        $start = [Diagnostics.ProcessStartInfo]::new()
        $start.FileName = $LaunchPath
        $start.ArgumentList.Add($argument)
        $start.UseShellExecute = $false
        $start.RedirectStandardOutput = $true
        $start.RedirectStandardError = $true
        $start.WorkingDirectory = Join-Path $profile 'project'
        $start.Environment.Clear()
        foreach ($pair in @{
            SystemRoot = $env:SystemRoot; WINDIR = $env:SystemRoot; COMSPEC = "$env:SystemRoot\System32\cmd.exe"
            PATH = "$env:SystemRoot\System32"; HOME = $profile; USERPROFILE = $profile
            APPDATA = (Join-Path $profile 'AppData/Roaming'); LOCALAPPDATA = (Join-Path $profile 'AppData/Local')
            TEMP = (Join-Path $profile 'Temp'); TMP = (Join-Path $profile 'Temp')
        }.GetEnumerator()) { $start.Environment[$pair.Key] = $pair.Value }
        $process = [Diagnostics.Process]::Start($start)
        try {
            $stdout = $process.StandardOutput.ReadToEndAsync()
            $stderr = $process.StandardError.ReadToEndAsync()
            if (-not $process.WaitForExit(180000)) {
                $process.Kill($true)
                throw "Timeout: $Phase $argument"
            }
            $text = $stdout.GetAwaiter().GetResult()
            $errorText = $stderr.GetAwaiter().GetResult()
            if ($process.ExitCode -ne 0) { throw "$Phase $argument failed ($($process.ExitCode)): $errorText" }
            if (-not $text.Trim()) { throw "$Phase $argument returned no output." }
            if ($argument -eq '--version') {
                $expected = "sodapop $($ReleaseInfo.Release.version)`nCopilot SDK $($ReleaseInfo.Release.copilot_sdk_version) / runtime $($ReleaseInfo.Release.copilot_runtime_version)"
                if ($text.Replace("`r", '').Trim() -cne $expected) { throw "Version mismatch: $text" }
            }
            $evidence.Add(@{ phase = $Phase; argument = $argument; exit_code = $process.ExitCode; output = $text.Trim() })
        } finally { $process.Dispose() }
    }
}

$portable = Join-Path $work 'portable'
Invoke-WindowsCtl @('portable', '--dir', $ReleaseDir, '--manifest', $manifestPath, '--output', $portable)
$binary = Join-Path $portable "sodapop-$($current.Release.version)-windows-amd64/sodapop.exe"
Test-CommandBytes $binary $current 'portable'
if ($InstalledBinary) {
    Test-CommandBytes (Get-RegularFile $InstalledBinary) $current 'channel-installed'
    if ($InstalledCommand) {
        $commandPath = (Get-Item -LiteralPath $InstalledCommand -Force).FullName
        Test-CommandBytes (Get-RegularFile $InstalledBinary) $current 'channel-command' $commandPath
    }
}

if ($MsiLifecycle) {
    foreach ($value in @($Msi, $MsiMetadata, $PreviousReleaseDir, $PreviousManifest, $PreviousMsi, $PreviousMsiMetadata)) {
        if (-not $value) { throw 'MSI lifecycle requires current and previous MSI, MSI metadata, release directories and final manifests.' }
    }
    $null = Get-Command msiexec.exe -CommandType Application -ErrorAction Stop
    $previous = Get-VerifiedRelease $PreviousReleaseDir $PreviousManifest
    if ($current.Release.version -notmatch '^\d+\.\d+\.\d+$' -or $previous.Release.version -notmatch '^\d+\.\d+\.\d+$' -or
        [version] $previous.Release.version -ge [version] $current.Release.version) {
        throw 'MSI upgrade needs two strictly increasing numeric release versions.'
    }

    function Read-MsiEvidence([string] $Package, [string] $Metadata, $ReleaseInfo) {
        $packagePath = Get-RegularFile $Package
        $record = Get-Content -LiteralPath (Get-RegularFile $Metadata) -Raw | ConvertFrom-Json
        if ($record.schema_version -ne 1 -or $record.scope -cne 'perUser' -or $record.architecture -cne 'x64' -or
            $record.version -cne $ReleaseInfo.Release.version -or $record.binary_sha256 -ine $ReleaseInfo.Artifact.binary_sha256 -or
            $record.source_archive_sha256 -ine $ReleaseInfo.Artifact.archive_sha256 -or
            $record.source_archive -cne $ReleaseInfo.Artifact.archive -or $record.msi -cne [IO.Path]::GetFileName($packagePath) -or
            $record.upgrade_code -cne '972F78B3-B6A5-5422-AABD-CA10FE7E6241' -or
            $record.product_code -notmatch '^[A-Fa-f0-9]{8}(-[A-Fa-f0-9]{4}){3}-[A-Fa-f0-9]{12}$') {
            throw 'MSI metadata differs from the final release or per-user contract.'
        }
        Assert-Hash $packagePath $record.msi_sha256
        $signature = Get-AuthenticodeSignature -LiteralPath $packagePath
        if ($record.signing_state -eq 'unsigned-candidate') {
            if (-not $AllowUnsignedCandidate -or $signature.Status -ne 'NotSigned') {
                throw 'Unsigned MSI testing requires explicit -AllowUnsignedCandidate and an unsigned container.'
            }
        } elseif ($record.signing_state -ne 'authenticode-verified' -or $signature.Status -ne 'Valid' -or
            $null -eq $signature.SignerCertificate -or $signature.SignerCertificate.Thumbprint -ine $record.signer_thumbprint) {
            throw 'MSI signing evidence is invalid.'
        }
        return @{ Path = $packagePath; Record = $record }
    }

    $old = Read-MsiEvidence $PreviousMsi $PreviousMsiMetadata $previous
    $new = Read-MsiEvidence $Msi $MsiMetadata $current
    if ($old.Record.product_code -eq $new.Record.product_code) { throw 'MSI product codes must change per version.' }
    $installer = New-Object -ComObject WindowsInstaller.Installer
    try {
        $relatedProducts = @($installer.RelatedProducts("{$($new.Record.upgrade_code)}"))
        if ($relatedProducts.Count -ne 0) {
            throw 'Refusing an existing registered or advertised Sodapop MSI, including versions outside the test pair.'
        }
    } finally {
        $null = [Runtime.InteropServices.Marshal]::ReleaseComObject($installer)
    }
    $installed = Join-Path $env:LOCALAPPDATA 'Programs/Sodapop'
    if (Test-Path -LiteralPath $installed) { throw 'Refusing a pre-existing Sodapop installation.' }
    $arpRoot = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall'
    foreach ($package in @($old, $new)) {
        if (Test-Path -LiteralPath "$arpRoot\{$($package.Record.product_code)}") { throw 'Product already registered.' }
    }
    $originalUserPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
    $originalMachinePath = [Environment]::GetEnvironmentVariable('PATH', 'Machine')
    if (@($originalUserPath -split ';' | Where-Object { $_.TrimEnd('\', '/') -ieq $installed.TrimEnd('\', '/') }).Count) {
        throw 'User PATH already contains the installation directory.'
    }
    $sentinelName = 'installer-sentinel-' + [guid]::NewGuid().ToString('N')
    $sentinels = @(
        (Join-Path $env:APPDATA "sodapop/$sentinelName"),
        (Join-Path $env:LOCALAPPDATA "sodapop/state/$sentinelName"),
        (Join-Path $env:LOCALAPPDATA "sodapop/cache/$sentinelName")
    )
    foreach ($sentinel in $sentinels) {
        $null = New-Item -ItemType Directory -Path ([IO.Path]::GetDirectoryName($sentinel)) -Force
        [IO.File]::WriteAllText($sentinel, $sentinelName)
    }

    function Assert-PreservedState {
        foreach ($sentinel in $sentinels) {
            if (-not (Test-Path -LiteralPath $sentinel) -or [IO.File]::ReadAllText($sentinel) -cne $sentinelName) {
                throw "User state was changed or deleted: $sentinel"
            }
        }
        if ([Environment]::GetEnvironmentVariable('PATH', 'Machine') -cne $originalMachinePath) {
            throw 'Per-user MSI modified the machine PATH.'
        }
    }

    function Invoke-Msi([string] $Operation, $Package, [string] $Phase) {
        $log = Join-Path $work "$Phase.log"
        # Reject quoting syntax before constructing msiexec's native command line.
        if ($Package.Path.Contains('"') -or $log.Contains('"')) { throw 'Invalid MSI/log path.' }
        $arguments = "$Operation `"$($Package.Path)`" /qn /norestart /L*V `"$log`""
        $process = Start-Process -FilePath "$env:SystemRoot\System32\msiexec.exe" -ArgumentList $arguments -PassThru
        try {
            if (-not $process.WaitForExit(300000)) {
                $process.Kill()
                throw "$Phase timed out; disposable runner must be discarded."
            }
            $code = $process.ExitCode
            if ($code -notin @(0, 3010)) { throw "$Phase failed with msiexec code $code; see $log" }
            $evidence.Add(@{ phase = $Phase; exit_code = $code; reboot_required = ($code -eq 3010); log = $log })
        } finally { $process.Dispose() }
        Assert-PreservedState
    }

    function Assert-Installed($Package, $ReleaseInfo, [string] $Phase) {
        $key = "$arpRoot\{$($Package.Record.product_code)}"
        $registration = Get-ItemProperty -LiteralPath $key
        if ($registration.DisplayName -cne 'Sodapop' -or $registration.DisplayVersion -cne $ReleaseInfo.Release.version) {
            throw 'Registered application name/version mismatch.'
        }
        $entries = @([Environment]::GetEnvironmentVariable('PATH', 'User') -split ';' |
            Where-Object { $_.TrimEnd('\', '/') -ieq $installed.TrimEnd('\', '/') })
        if ($entries.Count -ne 1) { throw 'Expected exactly one per-user PATH entry.' }
        foreach ($relative in @('README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md', 'LICENSES/copilot-runtime.license')) {
            $null = Get-RegularFile (Join-Path $installed $relative)
        }
        Test-CommandBytes (Join-Path $installed 'sodapop.exe') $ReleaseInfo $Phase
    }

    # A failed run leaves logs/state for diagnosis; discard the runner instead of
    # guessing which product to uninstall or recursively deleting user data.
    Invoke-Msi '/i' $old 'msi-install'
    Assert-Installed $old $previous 'msi-installed'
    Remove-Item -LiteralPath (Join-Path $installed 'sodapop.exe')
    Invoke-Msi '/fau' $old 'msi-repair'
    Assert-Installed $old $previous 'msi-repaired'
    Invoke-Msi '/i' $new 'msi-upgrade'
    Assert-Installed $new $current 'msi-upgraded'
    if (Test-Path -LiteralPath "$arpRoot\{$($old.Record.product_code)}") { throw 'Previous MSI remains registered after upgrade.' }
    Invoke-Msi '/x' $new 'msi-uninstall'
    if ((Test-Path -LiteralPath (Join-Path $installed 'sodapop.exe')) -or
        (Test-Path -LiteralPath "$arpRoot\{$($new.Record.product_code)}")) { throw 'Uninstall left the command or registered app.' }
    if ([Environment]::GetEnvironmentVariable('PATH', 'User') -cne $originalUserPath) {
        throw 'Uninstall did not restore the original user PATH exactly.'
    }
    Assert-PreservedState
}
Write-NewJson (Join-Path $work 'native-evidence.json') ([ordered]@{
    version = $current.Release.version
    native_platform = 'windows/amd64'
    source = $(if ($PublicDownload) { 'public-download' } else { 'local-candidate' })
    msi_lifecycle = [bool] $MsiLifecycle
    checks = $evidence.ToArray()
})
