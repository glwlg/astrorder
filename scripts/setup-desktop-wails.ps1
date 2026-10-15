param()

$ErrorActionPreference = 'Stop'
$Root = Split-Path $PSScriptRoot -Parent
$Versions = Get-Content (Join-Path $Root 'desktop-wails/toolchain.json') -Raw | ConvertFrom-Json
$Tools = Join-Path $Root '.tools/wails'
$NodeDir = Join-Path $Tools "node-v$($Versions.node)-win-x64"
$Node = Join-Path $NodeDir 'node.exe'
$Wails = Join-Path $Tools 'bin/wails3.exe'
$PreviousToolchain = $env:GOTOOLCHAIN
$PreviousBin = $env:GOBIN

function Assert-Exit([string]$Step) {
    if ($LASTEXITCODE -ne 0) { throw "$Step failed with exit code $LASTEXITCODE" }
}

try {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'Install Go from https://go.dev/dl/ first; Go toolchain auto-download must be available.' }
    $env:GOTOOLCHAIN = "go$($Versions.go)"
    & go version
    Assert-Exit 'Go toolchain'
    New-Item -ItemType Directory -Force -Path (Join-Path $Tools 'bin') | Out-Null
    if (-not (Test-Path $Node)) {
        $Scratch = Join-Path $Tools 'downloads'
        New-Item -ItemType Directory -Force -Path $Scratch | Out-Null
        $ArchiveName = "node-v$($Versions.node)-win-x64.zip"
        $Archive = Join-Path $Scratch $ArchiveName
        $Base = "https://nodejs.org/dist/v$($Versions.node)"
        $Sums = (Invoke-WebRequest "$Base/SHASUMS256.txt").Content
        $Line = @($Sums -split "`n" | Where-Object { $_.Trim().EndsWith(" $ArchiveName") })
        if ($Line.Count -ne 1) { throw 'Missing or ambiguous official Node checksum' }
        $Expected = ($Line[0].Trim() -split '\s+')[0]
        Invoke-WebRequest "$Base/$ArchiveName" -OutFile $Archive
        if ((Get-FileHash $Archive -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Expected) { throw 'Node SHA256 mismatch' }
        Expand-Archive -Path $Archive -DestinationPath $Tools -Force
        Remove-Item $Archive
    }
    $ActualNode = & $Node --version
    Assert-Exit 'Node version'
    if ($ActualNode.Trim() -ne "v$($Versions.node)") { throw "Unexpected Node version: $ActualNode" }
    $env:GOBIN = Join-Path $Tools 'bin'
    if (-not (Test-Path $Wails)) {
        & go install "github.com/wailsapp/wails/v3/cmd/wails3@$($Versions.wails)"
        Assert-Exit 'Wails CLI installation'
    }
    # Windows PowerShell 5 wraps native stderr as ErrorRecord even on exit 0.
    $ErrorActionPreference = 'Continue'
    try { $ActualWails = & $Wails version 2>&1 } finally { $ErrorActionPreference = 'Stop' }
    Assert-Exit 'Wails version'
    if (($ActualWails -join "`n").Trim() -ne $Versions.wails) { throw 'Unexpected Wails CLI version; remove only .tools/wails/bin/wails3.exe and rerun setup.' }
    & $Node (Join-Path $Root 'scripts/desktop-wails.mjs') install
    Assert-Exit 'Locked dependency installation'
    Write-Output "Ready: Node $ActualNode, Wails $($Versions.wails), Go $($Versions.go)"
} finally {
    $env:GOTOOLCHAIN = $PreviousToolchain
    $env:GOBIN = $PreviousBin
}
