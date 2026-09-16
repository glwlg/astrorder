[CmdletBinding()]
param([ValidateRange(1024, 65535)][int]$Port = 9335)

$ErrorActionPreference = 'Stop'

function Show-Error([string]$Message) {
  (New-Object -ComObject WScript.Shell).Popup($Message, 0, 'Codex CDP', 16) | Out-Null
}

function Save-CdpState {
  $version = Invoke-RestMethod -Uri "http://127.0.0.1:$Port/json/version" -TimeoutSec 1
  $uri = [Uri]$version.webSocketDebuggerUrl
  if ($uri.Host -ne '127.0.0.1' -or $uri.Port -ne $Port -or
      $uri.AbsolutePath -notmatch '^/devtools/browser/([A-Za-z0-9._-]{1,200})$') {
    throw 'Codex 返回了无效的 CDP 地址。'
  }
  $directory = Join-Path $env:LOCALAPPDATA 'AstrOrder\CodexCdp'
  New-Item -ItemType Directory -Force -Path $directory | Out-Null
  $path = Join-Path $directory 'state.json'
  $temporary = "$path.tmp"
  @{ port = $Port; browserId = $Matches[1]; createdAt = [DateTime]::UtcNow.ToString('o') } |
    ConvertTo-Json | Set-Content -LiteralPath $temporary -Encoding utf8
  Move-Item -LiteralPath $temporary -Destination $path -Force
}

try {
  $existing = Get-CimInstance Win32_Process -Filter "Name = 'ChatGPT.exe'" |
    Where-Object { $_.CommandLine -notmatch '(?:^|\s)--type=' } |
    Select-Object -First 1
  if ($existing) {
    try {
      Save-CdpState
      Start-Process "shell:AppsFolder\OpenAI.Codex_2p2nqsd0c76g0!App"
      exit 0
    } catch {
      Show-Error "Codex 已在运行，但未开放 CDP。请先退出 Codex，再从此快捷方式启动。"
      exit 1
    }
  }

  if (Get-NetTCPConnection -State Listen -LocalPort $Port -ErrorAction SilentlyContinue) {
    Show-Error "端口 $Port 已被其他程序占用。"
    exit 1
  }

  $package = Get-AppxPackage -Name OpenAI.Codex |
    Sort-Object Version -Descending |
    Select-Object -First 1
  if (-not $package) { throw '未找到已安装的 Codex。' }

  $executable = Join-Path $package.InstallLocation 'app\ChatGPT.exe'
  $profile = Join-Path $env:LOCALAPPDATA 'AstrOrder\CodexCdpProfile'
  New-Item -ItemType Directory -Force -Path $profile | Out-Null
  Start-Process -FilePath $executable -ArgumentList @(
    '--remote-debugging-address=127.0.0.1',
    "--remote-debugging-port=$Port",
    "--user-data-dir=$profile"
  )
  $deadline = [DateTime]::UtcNow.AddSeconds(20)
  while ([DateTime]::UtcNow -lt $deadline) {
    try { Save-CdpState; exit 0 } catch { Start-Sleep -Milliseconds 250 }
  }
  throw 'Codex 已启动，但 CDP 端点未在 20 秒内就绪。'
} catch {
  Show-Error $_.Exception.Message
  exit 1
}
