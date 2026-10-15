param(
    [string]$Version = "3.13"
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$ToolsDir = Join-Path $Root ".tools"
$NsisDir = Join-Path $ToolsDir "nsis"
$MakensisExe = Join-Path $NsisDir "makensis.exe"

if (Test-Path $MakensisExe) {
    Write-Host "NSIS already installed at $NsisDir"
    & $MakensisExe /VERSION
    exit 0
}

$TempDir = Join-Path $env:TEMP "nsis-download-$Version"
if (Test-Path $TempDir) {
    Remove-Item -Path $TempDir -Recurse -Force
}
New-Item -ItemType Directory -Path $TempDir | Out-Null

$ZipFile = Join-Path $TempDir "nsis-$Version.zip"
$Url = "https://downloads.sourceforge.net/project/nsis/NSIS%203/$Version/nsis-$Version.zip"

Write-Host "Downloading NSIS $Version from $Url ..."
$Curl = Get-Command curl.exe -ErrorAction SilentlyContinue
if ($Curl) {
    & $Curl.Source -f -L $Url -o $ZipFile
} else {
    Invoke-WebRequest -Uri $Url -OutFile $ZipFile -UseBasicParsing
}

Write-Host "Extracting NSIS $Version ..."
$ExtractDir = Join-Path $TempDir "extracted"
Expand-Archive -Path $ZipFile -DestinationPath $ExtractDir -Force

$SourceDir = Join-Path $ExtractDir "nsis-$Version"
if (-not (Test-Path $SourceDir)) {
    throw "Extracted directory does not contain nsis-$Version"
}

New-Item -ItemType Directory -Path $NsisDir -Force | Out-Null
Copy-Item -Path (Join-Path $SourceDir "*") -Destination $NsisDir -Recurse -Force

Remove-Item -Path $TempDir -Recurse -Force -ErrorAction SilentlyContinue

Write-Host "Verifying NSIS installation..."
& $MakensisExe /VERSION
Write-Host "NSIS $Version installed successfully to $NsisDir"
