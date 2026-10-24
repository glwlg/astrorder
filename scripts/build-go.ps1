[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$ExpectedBufVersion = '1.66.1'
$ExpectedGoVersion = 'go1.27.2'
$ExpectedProtocGenGoVersion = 'v1.36.12'
$ExpectedProtocGenGoGrpcVersion = '1.6.2'
$ExpectedGrpcToolsVersion = '1.84.0'

function Resolve-Tool {
    param([Parameter(Mandatory = $true)][string]$Requested)
    $hasSeparator = $Requested.Contains('/') -or $Requested.Contains('\')
    if ($hasSeparator) {
        if (-not (Test-Path -LiteralPath $Requested -PathType Leaf)) {
            throw "Required tool does not exist: $Requested"
        }
        return [IO.Path]::GetFullPath($Requested)
    }
    $command = Get-Command $Requested -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $command) {
        throw "Required tool is not on PATH: $Requested"
    }
    return $command.Source
}

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$Step,
        [Parameter(Mandatory = $true)][string]$Directory,
        [Parameter(Mandatory = $true)][string]$File,
        [Parameter()][string[]]$Arguments = @()
    )
    Write-Host "==> $Step"
    Push-Location $Directory
    try {
        & $File @Arguments
        $exitCode = $LASTEXITCODE
    }
    finally {
        Pop-Location
    }
    if ($exitCode -ne 0) {
        throw "$Step failed with exit code $exitCode."
    }
}

function Get-NativeOutput {
    param(
        [Parameter(Mandatory = $true)][string]$Step,
        [Parameter(Mandatory = $true)][string]$File,
        [Parameter()][string[]]$Arguments = @()
    )
    $output = (& $File @Arguments 2>&1 | Out-String).Trim()
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "$Step failed with exit code $exitCode.`n$output"
    }
    return $output
}

function Set-CleanBuildEnvironment {
    param([Parameter(Mandatory = $true)][hashtable]$Original)
    $allowed = @(
        'PATH', 'PATHEXT', 'SYSTEMROOT', 'WINDIR', 'TEMP', 'TMP', 'TMPDIR',
        'HOME', 'USERPROFILE', 'LOCALAPPDATA', 'GOCACHE', 'GOMODCACHE', 'GOPATH'
    )
    Get-ChildItem Env: | ForEach-Object { Remove-Item -LiteralPath ("Env:" + $_.Name) -ErrorAction SilentlyContinue }
    foreach ($name in $allowed) {
        if ($Original.ContainsKey($name) -and $Original[$name]) {
            Set-Item -LiteralPath ("Env:" + $name) -Value $Original[$name]
        }
    }
    $env:GOENV = 'off'
    $env:GOTOOLCHAIN = 'local'
}

function Restore-Environment {
    param([Parameter(Mandatory = $true)][hashtable]$Original)
    Get-ChildItem Env: | ForEach-Object { Remove-Item -LiteralPath ("Env:" + $_.Name) -ErrorAction SilentlyContinue }
    foreach ($entry in $Original.GetEnumerator()) {
        Set-Item -LiteralPath ("Env:" + $entry.Key) -Value $entry.Value
    }
}

$originalEnvironment = @{}
Get-ChildItem Env: | ForEach-Object { $originalEnvironment[$_.Name] = $_.Value }
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = [IO.Path]::GetFullPath((Join-Path $scriptDir '..'))
$contractsDir = Join-Path $repoRoot 'contracts'
$daemonDir = Join-Path $repoRoot 'session-daemon-go'
$appDir = Join-Path $repoRoot 'app-server-go'

$buf = Resolve-Tool $(if ($env:ASTRORDER_BUF) { $env:ASTRORDER_BUF } else { 'buf' })
$go = Resolve-Tool $(if ($env:ASTRORDER_GO) { $env:ASTRORDER_GO } else { 'go' })
$protocGenGo = Resolve-Tool $(if ($env:ASTRORDER_PROTOC_GEN_GO) { $env:ASTRORDER_PROTOC_GEN_GO } else { 'protoc-gen-go' })
$protocGenGoGrpc = Resolve-Tool $(if ($env:ASTRORDER_PROTOC_GEN_GO_GRPC) { $env:ASTRORDER_PROTOC_GEN_GO_GRPC } else { 'protoc-gen-go-grpc' })
$goOnlyRaw = if ($env:ASTRORDER_GO_ONLY) { $env:ASTRORDER_GO_ONLY } else { '1' }
if ($goOnlyRaw -notin @('0', '1')) {
    throw 'ASTRORDER_GO_ONLY must be 0 or 1.'
}
$goOnly = $goOnlyRaw -eq '1'
$python = $null
if (-not $goOnly) {
    $python = Resolve-Tool $(if ($env:ASTRORDER_GRPC_TOOLS_PYTHON) { $env:ASTRORDER_GRPC_TOOLS_PYTHON } else { 'python' })
}
$outputRoot = if ($env:ASTRORDER_GO_OUTPUT_DIR) {
    [IO.Path]::GetFullPath($env:ASTRORDER_GO_OUTPUT_DIR)
}
else {
    Join-Path $repoRoot '.runtime/go-build'
}
New-Item -ItemType Directory -Force -Path $outputRoot | Out-Null
$partialDir = Join-Path $outputRoot ('.astrorder-go-build.partial.' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $partialDir | Out-Null
$publishedDir = $null

try {
    Set-CleanBuildEnvironment $originalEnvironment

    $bufVersion = Get-NativeOutput 'Buf version check' $buf @('--version')
    if ($bufVersion -ne $ExpectedBufVersion) { throw "Buf $ExpectedBufVersion is required; got $bufVersion." }
    $goVersion = Get-NativeOutput 'Go version check' $go @('version')
    if ($goVersion -notmatch ([regex]::Escape(" $ExpectedGoVersion "))) { throw "Go $ExpectedGoVersion is required; got $goVersion." }
    $goPluginVersion = Get-NativeOutput 'protoc-gen-go version check' $protocGenGo @('--version')
    if ($goPluginVersion -notmatch ('^protoc-gen-go(?:\.exe)?\s+' + [regex]::Escape($ExpectedProtocGenGoVersion) + '$')) {
        throw "protoc-gen-go $ExpectedProtocGenGoVersion is required; got $goPluginVersion."
    }
    $grpcPluginVersion = Get-NativeOutput 'protoc-gen-go-grpc version check' $protocGenGoGrpc @('--version')
    if ($grpcPluginVersion -notmatch ('^protoc-gen-go-grpc\s+' + [regex]::Escape($ExpectedProtocGenGoGrpcVersion) + '$')) {
        throw "protoc-gen-go-grpc $ExpectedProtocGenGoGrpcVersion is required; got $grpcPluginVersion."
    }

    $env:ASTRORDER_VERIFY_GENERATION = '1'
    $env:ASTRORDER_BUF = $buf
    $env:ASTRORDER_PROTOC_GEN_GO = $protocGenGo
    $env:ASTRORDER_PROTOC_GEN_GO_GRPC = $protocGenGoGrpc
    if ($goOnly) {
        $env:ASTRORDER_GENERATE_PYTHON = '0'
    }
    else {
        Invoke-Native 'Python grpcio/grpcio-tools version check' $contractsDir $python @(
            '-c',
            "import grpc, importlib.metadata; assert grpc.__version__ == '$ExpectedGrpcToolsVersion', grpc.__version__; assert importlib.metadata.version('grpcio-tools') == '$ExpectedGrpcToolsVersion', importlib.metadata.version('grpcio-tools')"
        )
        $env:ASTRORDER_GENERATE_PYTHON = '1'
        $env:ASTRORDER_GRPC_TOOLS_PYTHON = $python
        $env:ASTRORDER_INTEROP_PYTHON = $python
    }

    Invoke-Native 'Buf lint' $contractsDir $buf @('lint')
    Invoke-Native 'Buf descriptor build' $contractsDir $buf @('build')
    Invoke-Native 'Contract tests and deterministic generation' $contractsDir $go @('test', './...', '-count=1')
    Invoke-Native 'Contract vet' $contractsDir $go @('vet', './...')
    Invoke-Native 'Session Daemon tests' $daemonDir $go @('test', './...', '-count=1')
    Invoke-Native 'Session Daemon vet' $daemonDir $go @('vet', './...')
    Invoke-Native 'App Server tests' $appDir $go @('test', '-p', '1', './...', '-count=1')
    Invoke-Native 'App Server vet' $appDir $go @('vet', './...')

    Invoke-Native 'Build astrorder-sessiond.exe' $daemonDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-sessiond.exe'), './cmd/astrorder-sessiond')
    Invoke-Native 'Build astrorder-app.exe' $appDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-app.exe'), './cmd/astrorder-app')
    Invoke-Native 'Build astrorderctl.exe' $appDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorderctl.exe'), './cmd/astrorderctl')
    Invoke-Native 'Build astrorder-agent.exe' $appDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-agent.exe'), './cmd/astrorder-agent')

    Invoke-Native 'Build astrorder-helper.exe' $appDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-helper.exe'), './cmd/astrorder-helper')
    Invoke-Native 'Build astrorder-model-helper.exe' $daemonDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-model-helper.exe'), './cmd/astrorder-model-helper')
    Invoke-Native 'Build astrorder-memory-helper.exe' $appDir $go @('build', '-trimpath', '-o', (Join-Path $partialDir 'astrorder-memory-helper.exe'), './cmd/astrorder-memory-helper')

    foreach ($helperArch in @('amd64', 'arm64')) {
        $helperPlatform = "linux-$helperArch"
        $helperDir = Join-Path $partialDir "helpers/$helperPlatform"
        New-Item -ItemType Directory -Path $helperDir | Out-Null
        $env:GOOS = 'linux'
        $env:GOARCH = $helperArch
        $env:CGO_ENABLED = '0'
        try {
            Invoke-Native "Build model helper for $helperPlatform" $daemonDir $go @('build', '-trimpath', '-o', (Join-Path $helperDir 'astrorder-model-helper'), './cmd/astrorder-model-helper')
        }
        finally {
            Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        }
        $env:GOOS = 'linux'
        $env:GOARCH = $helperArch
        $env:CGO_ENABLED = '0'
        try {
            Invoke-Native "Build target helper for $helperPlatform" $appDir $go @('build', '-trimpath', '-o', (Join-Path $helperDir 'astrorder-helper'), './cmd/astrorder-helper')
            Invoke-Native "Build session CLI for $helperPlatform" $appDir $go @('build', '-trimpath', '-o', (Join-Path $helperDir 'astrorder-agent'), './cmd/astrorder-agent')
        }
        finally {
            Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
        }
        $helperHash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $helperDir 'astrorder-model-helper')).Hash.ToLowerInvariant()
        [IO.File]::WriteAllText((Join-Path $helperDir 'SHA256SUMS'), "$helperHash  astrorder-model-helper`n", [Text.UTF8Encoding]::new($false))
    }

    $artifactNames = @(
        'astrorder-sessiond.exe',
        'astrorder-app.exe',
        'astrorderctl.exe',
        'astrorder-agent.exe',

        'astrorder-helper.exe',
        'astrorder-model-helper.exe',
        'astrorder-memory-helper.exe',
        'helpers/linux-amd64/astrorder-model-helper',
        'helpers/linux-arm64/astrorder-model-helper',
        'helpers/linux-amd64/astrorder-helper',
        'helpers/linux-arm64/astrorder-helper',
        'helpers/linux-amd64/astrorder-agent',
        'helpers/linux-arm64/astrorder-agent'
    )
    $manifestLines = foreach ($name in $artifactNames) {
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $partialDir $name)).Hash.ToLowerInvariant()
        "$hash  $name"
    }
    [IO.File]::WriteAllLines((Join-Path $partialDir 'SHA256SUMS'), $manifestLines, [Text.UTF8Encoding]::new($false))

    $candidateName = 'candidate-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssZ') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
    $publishedDir = Join-Path $outputRoot $candidateName
    if (Test-Path -LiteralPath $publishedDir) {
        throw "Refusing to replace existing output: $publishedDir"
    }
    Move-Item -LiteralPath $partialDir -Destination $publishedDir
    $partialDir = $null
    Write-Host "Candidate artifacts retained at $publishedDir"
    Write-Host "SHA-256 manifest: $(Join-Path $publishedDir 'SHA256SUMS')"
}
finally {
    Restore-Environment $originalEnvironment
    if ($partialDir -and (Test-Path -LiteralPath $partialDir)) {
        Remove-Item -LiteralPath $partialDir -Recurse -Force
    }
}
