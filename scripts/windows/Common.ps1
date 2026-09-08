#requires -Version 7.2
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Assert-WindowsX64 {
    if (-not $IsWindows -or [Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'X64') {
        throw 'This operation requires native Windows x64; cross-compilation is not native evidence.'
    }
}

function Assert-DisposableRunner {
    Assert-WindowsX64
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
        if (-not $parent.PSIsContainer -or ($parent.Attributes -band [IO.FileAttributes]::ReparsePoint)) {
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
    $null = Get-RegularFile $env:SODAPOP_RELEASECTL
    Invoke-Checked $tool $Arguments
}

function Assert-Hash([string] $Path, [string] $Expected) {
    if ($Expected -notmatch '^[0-9a-fA-F]{64}$') { throw "Invalid expected SHA256 for $Path" }
    $file = Get-RegularFile $Path
    if ((Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash -ine $Expected) {
        throw "SHA256 mismatch: $file"
    }
}

function Get-VerifiedRelease([string] $Directory, [string] $Manifest) {
    $manifestPath = Get-RegularFile $Manifest
    $tool = Get-RegularFile $env:SODAPOP_RELEASECTL
    Invoke-Checked $tool @('verify', '--dir', $Directory, '--manifest', $manifestPath, '--platform', 'windows/amd64') | Out-Host
    $release = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($release.schema_version -ne 1 -or $release.version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$') {
        throw 'Invalid release metadata.'
    }
    $artifacts = @($release.artifacts | Where-Object platform -CEQ 'windows/amd64')
    if ($artifacts.Count -ne 1 -or $artifacts[0].archive -cne "sodapop-$($release.version)-windows-amd64.zip") {
        throw 'Missing or mismatched Windows ZIP artifact.'
    }
    return @{ Release = $release; Artifact = $artifacts[0] }
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
