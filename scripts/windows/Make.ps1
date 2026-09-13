param(
    [Parameter(Position = 0)]
    [string] $Target = 'help',

    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]] $RemainingArguments
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$RepositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))

function Get-ApplicationPath {
    param(
        [string] $Name,
        [string[]] $Candidates
    )

    $command = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue |
        Select-Object -First 1
    if ($null -ne $command) {
        return $command.Source
    }
    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    return $null
}

function Initialize-Go {
    $go = Get-ApplicationPath 'go' @(
        (Join-Path $env:ProgramFiles 'Go\bin\go.exe'),
        (Join-Path $env:LOCALAPPDATA 'Programs\Go\bin\go.exe')
    )
    if (-not $go) {
        throw 'Go 1.27.1 or later was not found. Install Go, then open a new terminal.'
    }
    $goDirectory = Split-Path -Parent $go
    if (($env:PATH -split ';') -notcontains $goDirectory) {
        $env:PATH = $goDirectory + ';' + $env:PATH
    }
}

function Get-GitBash {
    if ($env:SODAPOP_BASH) {
        if (-not (Test-Path -LiteralPath $env:SODAPOP_BASH -PathType Leaf)) {
            throw "SODAPOP_BASH does not point to a file: $env:SODAPOP_BASH"
        }
        return [IO.Path]::GetFullPath($env:SODAPOP_BASH)
    }

    $candidates = @(
        (Join-Path $env:ProgramFiles 'Git\bin\bash.exe')
    )
    if (${env:ProgramFiles(x86)}) {
        $candidates += Join-Path ${env:ProgramFiles(x86)} 'Git\bin\bash.exe'
    }
    $git = Get-ApplicationPath 'git' @()
    if ($git) {
        $candidates += [IO.Path]::GetFullPath((Join-Path (Split-Path -Parent $git) '..\bin\bash.exe'))
    }

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }
    throw 'Git Bash was not found. Install Git for Windows, then open a new terminal.'
}

function Import-SodapopEnvironment {
    if ($env:SODAPOP_GITHUB_CLIENT_ID) {
        return
    }
    $environmentFile = Join-Path $RepositoryRoot '.sodapop.env'
    if (-not (Test-Path -LiteralPath $environmentFile -PathType Leaf)) {
        return
    }
    foreach ($line in Get-Content -LiteralPath $environmentFile) {
        if ($line -match '^\s*SODAPOP_GITHUB_CLIENT_ID\s*=\s*(.*?)\s*$') {
            $value = $Matches[1]
            if (($value.StartsWith('"') -and $value.EndsWith('"')) -or
                ($value.StartsWith("'") -and $value.EndsWith("'"))) {
                $value = $value.Substring(1, $value.Length - 2)
            }
            $env:SODAPOP_GITHUB_CLIENT_ID = $value
            return
        }
    }
}

function Invoke-Build {
    Initialize-Go
    Import-SodapopEnvironment
    $bash = Get-GitBash
    & $bash 'scripts/build.sh'
    if ($LASTEXITCODE -ne 0) {
        exit $LASTEXITCODE
    }
}

function Invoke-Start {
    Invoke-Build
    $output = if ($env:SODAPOP_OUTPUT) { $env:SODAPOP_OUTPUT } else { 'bin\sodapop.exe' }
    if (-not [IO.Path]::IsPathRooted($output)) {
        $output = Join-Path $RepositoryRoot $output
    }
    if (-not (Test-Path -LiteralPath $output -PathType Leaf)) {
        throw "Built executable was not found: $output"
    }
    & $output
    exit $LASTEXITCODE
}

if ($null -ne $RemainingArguments -and @($RemainingArguments).Count -gt 0) {
    throw "Unexpected arguments: $($RemainingArguments -join ' ')"
}

Push-Location $RepositoryRoot
try {
    switch ($Target.ToLowerInvariant()) {
        'build' { Invoke-Build }
        'start' { Invoke-Start }
        'run' { Invoke-Start }
        'help' {
            @'
Windows development commands:
  make build    Build bin\sodapop.exe with the bundled Copilot runtime
  make start    Rebuild and start Sodapop in the current terminal
  make run      Alias for make start
  make help     Show this help
'@ | Write-Host
        }
        default { throw "Unsupported target '$Target'. Run 'make help' for available targets." }
    }
} finally {
    Pop-Location
}
