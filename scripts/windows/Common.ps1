#requires -Version 7.2
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Get-NativeWindowsPlatform {
    if (-not $IsWindows) { throw 'This operation requires native Windows.' }
    $architecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    if ($architecture -eq [Runtime.InteropServices.Architecture]::X64) { return 'windows/amd64' }
    if ($architecture -eq [Runtime.InteropServices.Architecture]::Arm64) { return 'windows/arm64' }
    throw "Unsupported Windows architecture: $architecture"
}

function Convert-WindowsArchitectureToPlatform([string] $Architecture) {
    if ($Architecture -ceq 'x64') { return 'windows/amd64' }
    if ($Architecture -ceq 'arm64') { return 'windows/arm64' }
    throw "Unsupported Windows architecture selection: $Architecture"
}

function Convert-WindowsPlatformToArchitecture([string] $Platform) {
    if ($Platform -ceq 'windows/amd64') { return 'x64' }
    if ($Platform -ceq 'windows/arm64') { return 'arm64' }
    throw "Unsupported Windows platform: $Platform"
}

function Assert-NativeWindowsArchitecture([string] $ExpectedPlatform) {
    $native = Get-NativeWindowsPlatform
    $processArchitecture = [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture
    $osArchitecture = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    if ($processArchitecture -ne $osArchitecture) {
        throw "This operation requires a native $osArchitecture PowerShell process; emulation is not native evidence."
    }
    if ($native -cne $ExpectedPlatform) {
        throw "Expected native $ExpectedPlatform, but this host is $native."
    }
}

function Get-WindowsExecutablePlatform([string] $Path) {
    $file = Get-RegularFile $Path
    $stream = [IO.File]::Open($file, [IO.FileMode]::Open, [IO.FileAccess]::Read, [IO.FileShare]::Read)
    $reader = [IO.BinaryReader]::new($stream)
    try {
        if ($stream.Length -lt 64 -or $reader.ReadUInt16() -ne 0x5A4D) {
            throw "Expected a Windows PE executable: $file"
        }
        $stream.Position = 0x3C
        $header = $reader.ReadInt32()
        if ($header -lt 64 -or $header -gt $stream.Length - 6) {
            throw "Invalid Windows PE header offset: $file"
        }
        $stream.Position = $header
        if ($reader.ReadUInt32() -ne 0x00004550) {
            throw "Invalid Windows PE signature: $file"
        }
        $machine = $reader.ReadUInt16()
        if ($machine -eq 0x8664) { return 'windows/amd64' }
        if ($machine -eq 0xAA64) { return 'windows/arm64' }
        throw ('Unsupported Windows PE machine 0x{0:X4}: {1}' -f $machine, $file)
    } finally {
        $reader.Dispose()
    }
}

function Assert-WindowsExecutableArchitecture([string] $Path, [string] $ExpectedPlatform) {
    $actual = Get-WindowsExecutablePlatform $Path
    if ($actual -cne $ExpectedPlatform) {
        throw "Expected $ExpectedPlatform executable, but found $actual`: $Path"
    }
}

function Assert-DisposableRunner([string] $ExpectedPlatform) {
    Assert-NativeWindowsArchitecture $ExpectedPlatform
    if ($env:SODAPOP_WINDOWS_DISPOSABLE -cne '1') {
        throw 'Set SODAPOP_WINDOWS_DISPOSABLE=1 only in a dedicated disposable Windows user/runner.'
    }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = [Security.Principal.WindowsPrincipal]::new($identity)
    if ($principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'Run installer lifecycle tests as a non-elevated user; admin rights would mask per-user failures.'
    }
}

function Get-RegularFile([string] $Path) {
    $item = Get-Item -LiteralPath $Path -Force
    if ($item.PSIsContainer -or ($item.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
        throw "Expected a regular file, not a link: $Path"
    }
    return $item.FullName
}

function Get-NewDirectoryPath([string] $Path) {
    $full = [IO.Path]::GetFullPath($Path)
    if (Test-Path -LiteralPath $full) { throw "Output already exists: $full" }
    $parent = Get-Item -LiteralPath ([IO.Path]::GetDirectoryName($full)) -Force
    while ($null -ne $parent) {
        if (-not ($parent.Attributes -band [IO.FileAttributes]::Directory) -or
            ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "Output parent must be a real directory: $($parent.FullName)"
        }
        $parent = $parent.Parent
    }
    if ($full.Length -gt 160) { throw 'Use a shorter output path (maximum 160 characters).' }
    return $full
}

function Invoke-Checked([string] $Executable, [string[]] $Arguments) {
    $global:LASTEXITCODE = 0
    & $Executable @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Executable exited with code $LASTEXITCODE" }
}

function Invoke-WindowsCtl([string[]] $Arguments) {
    if (-not $env:SODAPOP_WINDOWSCTL -or -not $env:SODAPOP_RELEASECTL) {
        throw 'Build native windowsctl/releasectl helpers and set SODAPOP_WINDOWSCTL and SODAPOP_RELEASECTL.'
    }
    $tool = Get-RegularFile $env:SODAPOP_WINDOWSCTL
    $releaseTool = Get-RegularFile $env:SODAPOP_RELEASECTL
    $native = Get-NativeWindowsPlatform
    Assert-WindowsExecutableArchitecture $tool $native
    Assert-WindowsExecutableArchitecture $releaseTool $native
    Invoke-Checked $tool $Arguments
}

function Assert-Hash([string] $Path, [string] $Expected) {
    if ($Expected -notmatch '^[0-9a-fA-F]{64}$') { throw "Invalid expected SHA256 for $Path" }
    $file = Get-RegularFile $Path
    if ((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash -ine $Expected) {
        throw "SHA256 mismatch: $file"
    }
}

function Get-VerifiedRelease([string] $Directory, [string] $Manifest, [string] $Platform) {
    $architecture = Convert-WindowsPlatformToArchitecture $Platform
    $manifestPath = Get-RegularFile $Manifest
    $tool = Get-RegularFile $env:SODAPOP_RELEASECTL
    Assert-WindowsExecutableArchitecture $tool (Get-NativeWindowsPlatform)
    Invoke-Checked $tool @('verify', '--dir', $Directory, '--manifest', $manifestPath, '--platform', $Platform) | Out-Host
    $release = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($release.schema_version -ne 1 -or $release.version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$') {
        throw 'Invalid release metadata.'
    }
    $artifacts = @($release.artifacts | Where-Object platform -CEQ $Platform)
    $archivePlatform = $Platform.Replace('/', '-')
    if ($artifacts.Count -ne 1 -or $artifacts[0].archive -cne "sodapop-$($release.version)-$archivePlatform.zip") {
        throw 'Missing or mismatched Windows ZIP artifact.'
    }
    return @{
        Release = $release
        Artifact = $artifacts[0]
        Platform = $Platform
        Architecture = $architecture
        ArchiveRoot = "sodapop-$($release.version)-$archivePlatform"
    }
}

function Assert-StagedPayload([string] $Stage, $InputMetadata) {
    $payload = Join-Path $Stage 'payload'
    $files = @(Get-ChildItem -LiteralPath $payload -File -Recurse -Force)
    if ($files.Count -ne $InputMetadata.files.Count) { throw 'Staged payload has missing or extra files.' }
    foreach ($entry in $InputMetadata.files) {
        if ($entry.path -notmatch '^[A-Za-z0-9_@+.-]+(/[A-Za-z0-9_@+.-]+)*$' -or $entry.path -match '(^|/)\.\.?(/|$)') {
            throw 'Invalid staged payload path.'
        }
        Assert-Hash (Join-Path $payload $entry.path) $entry.sha256
    }
    foreach ($entry in Get-ChildItem -LiteralPath $payload -Recurse -Force) {
        if ($entry.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'Staged payload contains a link.' }
    }
    Assert-Hash (Join-Path $payload 'sodapop.exe') $InputMetadata.binary_sha256
}

function Write-NewJson([string] $Path, $Value) {
    $text = $Value | ConvertTo-Json -Depth 12
    $stream = [IO.File]::Open($Path, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
    try {
        $bytes = [Text.UTF8Encoding]::new($false).GetBytes($text + "`n")
        $stream.Write($bytes, 0, $bytes.Length)
    } finally {
        $stream.Dispose()
    }
}
