import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, openSync, readdirSync, readFileSync, readSync, closeSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { verifyBuiltPluginDist } from './build-official-plugins.mjs'
import { verifyFrontendDist } from './verify-frontend-dist.mjs'

const WSL_POWERSHELL = '/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe'
const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

export function isWindowsHost(platform = process.platform) {
  return platform === 'win32'
}

export function isWslHost(platform = process.platform) {
  return platform === 'linux' && existsSync(WSL_POWERSHELL)
}

export function toWindowsPath(input) {
  const value = String(input || '').trim()
  if (!value) return ''
  if (/^[a-zA-Z]:[\\/]/.test(value)) {
    return value.replace(/\//g, '\\')
  }
  const match = /^\/mnt\/([a-zA-Z])(?:\/(.*))?$/.exec(value)
  if (match) {
    const drive = match[1].toUpperCase()
    const rest = (match[2] || '').replace(/\//g, '\\')
    return rest ? `${drive}:\\${rest}` : `${drive}:\\`
  }
  if (value.startsWith('/') && isWslHost()) {
    const res = spawnSync('wslpath', ['-w', value], { encoding: 'utf8' })
    if (res.status === 0 && res.stdout.trim()) {
      return res.stdout.trim()
    }
  }
  return value
}

export function toHostPath(input, platform = process.platform) {
  const value = String(input || '').trim()
  if (!value) return ''
  if (platform === 'win32') return value
  const match = /^([a-zA-Z]):[\\/](.*)$/.exec(value)
  if (match) {
    const drive = match[1].toLowerCase()
    const rest = match[2].replace(/\\/g, '/')
    return `/mnt/${drive}/${rest}`
  }
  return value
}

export function normalizeWinPath(p) {
  return toWindowsPath(p).replace(/\\+$/, '').toLowerCase()
}

export function sha256File(filePath) {
  return createHash('sha256').update(readFileSync(filePath)).digest('hex')
}

export function assertPeExecutable(filePath, label = 'Executable') {
  if (!filePath || !existsSync(filePath)) {
    throw new Error(`${label} does not exist: ${filePath}`)
  }
  const info = statSync(filePath)
  if (!info.isFile() || info.size < 64) {
    throw new Error(`${label} is not a valid executable file: ${filePath}`)
  }
  const fd = openSync(filePath, 'r')
  try {
    const magic = Buffer.alloc(2)
    readSync(fd, magic, 0, 2, 0)
    if (magic[0] !== 0x4d || magic[1] !== 0x5a) {
      throw new Error(`${label} is missing Windows PE (MZ) header: ${filePath}`)
    }
  } finally {
    closeSync(fd)
  }
  return { path: filePath, size: info.size, sha256: sha256File(filePath) }
}

export function assertPackagedBinaryCandidate(exePath) {
  const base = path.basename(String(exePath || '')).toLowerCase()
  if (!base.endsWith('.exe')) {
    throw new Error(`Desktop binary candidate must end with .exe (needed for Windows --check): ${exePath}`)
  }
  if (base === 'astrorder-desktop-dev.exe' || base.includes('-dev.exe')) {
    throw new Error(`Refusing development desktop binary (${base}); build with "node scripts/desktop-wails.mjs build-packaged" instead`)
  }
  return assertPeExecutable(exePath, 'Packaged desktop binary')
}

export function assertDesktopCheckOutput(
  rawOutput,
  { expectedStateDir, requirePackaged = true, requireKernelManagement = true } = {},
) {
  const text = String(rawOutput || '').trim()
  if (!text) {
    throw new Error('Desktop binary --check produced empty output (ensure stdout was captured with "| Out-String")')
  }
  let info
  try {
    info = JSON.parse(text)
  } catch (err) {
    throw new Error(`Desktop binary --check returned invalid JSON: ${text.slice(0, 200)} (${err.message})`)
  }
  const stateDir = String(info?.stateDir || '').trim()
  if (!stateDir) {
    throw new Error(`Desktop --check missing stateDir: ${text}`)
  }
  if (stateDir.toLowerCase().includes('astrorder-wails-dev')) {
    throw new Error(`Desktop binary is in DEV mode (stateDir=${stateDir}); rebuild with -X main.defaultPackaged=true`)
  }
  if (requirePackaged && info?.packaged === false) {
    throw new Error(`Desktop binary reported packaged=false (stateDir=${stateDir}); rebuild with "node scripts/desktop-wails.mjs build-packaged"`)
  }
  if (expectedStateDir && normalizeWinPath(stateDir) !== normalizeWinPath(expectedStateDir)) {
    throw new Error(`Desktop --check stateDir mismatch: got "${stateDir}", expected "${expectedStateDir}"`)
  }
  if (!expectedStateDir && !/[\\/]astrorder$/i.test(stateDir)) {
    throw new Error(`Desktop --check stateDir does not point to production Astrorder directory: "${stateDir}"`)
  }
  if (requireKernelManagement && info?.kernelManagement !== true) {
    throw new Error(`Desktop --check reported kernelManagement=${info?.kernelManagement} (astrorderctl missing or unreachable)`)
  }
  return info
}

export function assertAppServiceStatus(rawOutput, { expectedExecutable } = {}) {
  const text = String(rawOutput || '').trim()
  if (!text) {
    throw new Error('astrorderctl service app status produced empty output')
  }
  let status
  try {
    status = JSON.parse(text)
  } catch (err) {
    throw new Error(`astrorderctl service app status returned invalid JSON: ${text.slice(0, 200)} (${err.message})`)
  }
  if (status?.ok !== true || status?.state !== 'running') {
    throw new Error(`App kernel is not running: ok=${status?.ok}, state=${status?.state}`)
  }
  if (status?.healthy !== true) {
    throw new Error(`App kernel health check failed: healthy=${status?.healthy}`)
  }
  if (status?.identity_verified !== true || status?.port_owned !== true) {
    throw new Error(
      `App kernel identity/port check failed: identity_verified=${status?.identity_verified}, port_owned=${status?.port_owned}`,
    )
  }
  const actualExe = status?.process?.executable_path || ''
  if (expectedExecutable && actualExe && normalizeWinPath(actualExe) !== normalizeWinPath(expectedExecutable)) {
    throw new Error(`App kernel running from unexpected path: got "${actualExe}", expected "${expectedExecutable}"`)
  }
  return status
}

export function assertDaemonServiceStatus(rawOutput, { expectedPid } = {}) {
  const text = String(rawOutput || '').trim()
  if (!text) {
    throw new Error('astrorderctl service daemon status produced empty output')
  }
  let status
  try {
    status = JSON.parse(text)
  } catch (err) {
    throw new Error(`astrorderctl service daemon status returned invalid JSON: ${text.slice(0, 200)} (${err.message})`)
  }
  if (status?.ok !== true || status?.state !== 'running') {
    throw new Error(`Session daemon is not running: ok=${status?.ok}, state=${status?.state}`)
  }
  if (status?.identity_verified !== true || status?.port_owned !== true) {
    throw new Error(
      `Session daemon identity/port check failed: identity_verified=${status?.identity_verified}, port_owned=${status?.port_owned}`,
    )
  }
  const pid = Number(status?.process?.pid || 0)
  if (!Number.isInteger(pid) || pid <= 0) {
    throw new Error(`Session daemon status missing valid PID: ${text}`)
  }
  if (expectedPid != null && Number(expectedPid) > 0 && pid !== Number(expectedPid)) {
    throw new Error(`Session daemon PID changed during deploy! before=${expectedPid}, after=${pid}`)
  }
  return status
}

export function assertDesktopProcess(processes, { expectedExePath, requireWindow = true } = {}) {
  const list = Array.isArray(processes) ? processes : processes ? [processes] : []
  if (list.length === 0) {
    throw new Error('Desktop client Astrorder.exe is not running')
  }
  const expectedNorm = expectedExePath ? normalizeWinPath(expectedExePath) : ''
  const matching = expectedNorm
    ? list.filter((p) => normalizeWinPath(p?.Path || '') === expectedNorm)
    : list
  if (matching.length === 0) {
    const foundPaths = list.map((p) => p?.Path || '(unknown)').join(', ')
    throw new Error(`No Astrorder process matches expected path "${expectedExePath}"; found: ${foundPaths}`)
  }
  const interactive = matching.find((p) => Number(p?.SessionId) >= 1)
  if (!interactive) {
    throw new Error(`Astrorder process is not running in an interactive desktop session (SessionId >= 1)`)
  }
  if (requireWindow) {
    const withWindow = matching.find((p) => Number(p?.SessionId) >= 1 && String(p?.MainWindowTitle || '').trim().length > 0)
    if (!withWindow) {
      throw new Error('Astrorder desktop process has no visible MainWindowTitle (client window failed to initialize or launched headless)')
    }
    return withWindow
  }
  return interactive
}

export function psQuote(value) {
  return `'${String(value).replace(/'/g, "''")}'`
}

export function runPowerShell(script) {
  const exe = isWindowsHost() ? 'powershell.exe' : isWslHost() ? WSL_POWERSHELL : ''
  if (!exe) {
    throw new Error('Windows PowerShell is required to verify a live Windows Astrorder installation')
  }
  const fullScript = `[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; $ErrorActionPreference = 'Stop'; ${script}`
  const res = spawnSync(exe, ['-NonInteractive', '-InputFormat', 'None', '-NoProfile', '-Command', fullScript], {
    encoding: 'utf8',
    stdio: ['ignore', 'pipe', 'pipe'],
    timeout: 45_000,
  })
  if (res.error) throw res.error
  if (res.status !== 0) {
    throw new Error(`PowerShell exited ${res.status}: ${(res.stderr || res.stdout || '').trim()}`)
  }
  return (res.stdout || '').trim()
}

export function inferInstallDirFromExePath(exePath) {
  const win = toWindowsPath(exePath).replace(/\\+$/, '')
  if (!win) return ''
  const lower = win.toLowerCase()
  if (lower.endsWith('\\astrorder.exe')) {
    return win.slice(0, -'\\Astrorder.exe'.length)
  }
  const serverBinMatch = /^(.*)\\server\\bin\\[^\\]+\.exe$/i.exec(win)
  if (serverBinMatch) {
    return serverBinMatch[1]
  }
  return ''
}

export function selectInstallDir({ override, liveExePaths = [], registryExePath = '', uninstallLocation = '' } = {}) {
  const liveDirs = new Map()
  for (const exePath of liveExePaths) {
    const dir = inferInstallDirFromExePath(exePath)
    if (dir) {
      const key = normalizeWinPath(dir)
      if (!liveDirs.has(key)) liveDirs.set(key, dir)
    }
  }
  if (liveDirs.size > 1) {
    throw new Error(
      `Multiple live Astrorder installations detected (${[...liveDirs.values()].join(', ')}); resolve conflict or pass --install-dir explicitly`,
    )
  }
  const liveDir = liveDirs.size === 1 ? [...liveDirs.values()][0] : ''

  if (override) {
    const overrideWin = toWindowsPath(override).replace(/\\+$/, '')
    if (liveDir && normalizeWinPath(liveDir) !== normalizeWinPath(overrideWin)) {
      throw new Error(
        `Refusing deploy/verify to "${overrideWin}": live Astrorder processes are running from "${liveDir}"`,
      )
    }
    return overrideWin
  }

  if (liveDir) {
    return liveDir
  }

  const regDir = inferInstallDirFromExePath(registryExePath) || toWindowsPath(uninstallLocation).replace(/\\+$/, '')
  if (regDir) {
    return regDir
  }

  throw new Error(
    'Unable to determine installed Astrorder directory from running processes or registry; pass --install-dir explicitly',
  )
}

export function resolveInstalledEnv(installDirOverride) {
  const script = `
    $localAppData = $env:LOCALAPPDATA
    $stateDir = Join-Path $localAppData 'Astrorder'
    $regExe = ''
    $appKey = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\App Paths\\Astrorder.exe'
    if (Test-Path -LiteralPath $appKey) {
      $val = (Get-Item -LiteralPath $appKey).GetValue('')
      if ($val) { $regExe = [string]$val }
    }
    $uninstLoc = ''
    $uninstKey = 'HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\Astrorder'
    if (Test-Path -LiteralPath $uninstKey) {
      $val = (Get-ItemProperty -LiteralPath $uninstKey -ErrorAction SilentlyContinue).InstallLocation
      if ($val) { $uninstLoc = [string]$val }
    }
    $livePaths = @(Get-Process -Name astrorder-sessiond,astrorder-app,Astrorder -ErrorAction SilentlyContinue | ForEach-Object {
      try { if ($_.Path) { $_.Path } } catch {}
    })
    @{
      stateDir = $stateDir
      registryExePath = $regExe
      uninstallLocation = $uninstLoc
      liveExePaths = $livePaths
    } | ConvertTo-Json -Depth 4 -Compress
  `
  const parsed = JSON.parse(runPowerShell(script))
  const installDirWin = selectInstallDir({
    override: installDirOverride,
    liveExePaths: Array.isArray(parsed.liveExePaths) ? parsed.liveExePaths : parsed.liveExePaths ? [parsed.liveExePaths] : [],
    registryExePath: parsed.registryExePath || '',
    uninstallLocation: parsed.uninstallLocation || '',
  })
  return {
    installDirWin,
    installDirHost: toHostPath(installDirWin),
    stateDirWin: parsed.stateDir,
    stateDirHost: toHostPath(parsed.stateDir),
  }
}

export function runDesktopCheckOnWindows(exePath) {
  const winExe = toWindowsPath(exePath)
  const raw = runPowerShell(`(& ${psQuote(winExe)} --check | Out-String).Trim()`)
  return raw
}

export function verifyDesktopCandidate(exePath, { expectedStateDir, allowUninstalled = false } = {}) {
  const hostPath = toHostPath(exePath)
  const fileInfo = assertPackagedBinaryCandidate(hostPath)
  const raw = runDesktopCheckOnWindows(hostPath)
  const check = assertDesktopCheckOutput(raw, {
    expectedStateDir,
    requirePackaged: true,
    requireKernelManagement: !allowUninstalled,
  })
  return { ...fileInfo, check }
}

export function queryLiveInstallation(installDirWin, stateDirWin) {
  const serverRootWin = `${installDirWin}\\server`
  const ctlWin = `${serverRootWin}\\bin\\astrorderctl.exe`
  const desktopExeWin = `${installDirWin}\\Astrorder.exe`
  const script = `
    $desktopCheck = (& ${psQuote(desktopExeWin)} --check | Out-String).Trim()
    $appStatus = (& ${psQuote(ctlWin)} service app status --root ${psQuote(serverRootWin)} --state-dir ${psQuote(stateDirWin)} | Out-String).Trim()
    $daemonStatus = (& ${psQuote(ctlWin)} service daemon status --root ${psQuote(serverRootWin)} --state-dir ${psQuote(stateDirWin)} | Out-String).Trim()
    $procs = @(Get-Process -Name Astrorder -ErrorAction SilentlyContinue | ForEach-Object {
      @{
        Name = $_.Name
        Id = $_.Id
        SessionId = $_.SessionId
        MainWindowTitle = $_.MainWindowTitle
        Path = $_.Path
      }
    })
    @{
      desktopCheck = $desktopCheck
      appStatus = $appStatus
      daemonStatus = $daemonStatus
      desktopProcesses = $procs
    } | ConvertTo-Json -Depth 5 -Compress
  `
  return JSON.parse(runPowerShell(script))
}

function discoverBuiltPluginDirs(rootDir, label = 'Official plugins directory') {
  if (!rootDir || !existsSync(rootDir)) {
    throw new Error(`${label} does not exist: ${rootDir}`)
  }
  const stat = statSync(rootDir)
  if (!stat.isDirectory()) {
    throw new Error(`${label} is not a directory: ${rootDir}`)
  }
  const dirs = readdirSync(rootDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && !entry.name.startsWith('.'))
    .map((entry) => path.join(rootDir, entry.name))
    .filter((dir) => existsSync(path.join(dir, 'plugin.json')))
    .sort()
  if (dirs.length === 0) {
    throw new Error(`No official plugins found in ${label}: ${rootDir}`)
  }
  return dirs
}

function verifyBuiltPluginMap(rootDir, label) {
  const dirs = discoverBuiltPluginDirs(rootDir, label)
  const byId = new Map()
  for (const dir of dirs) {
    const verified = verifyBuiltPluginDist(dir)
    if (byId.has(verified.id)) {
      throw new Error(`Duplicate official plugin id "${verified.id}" under ${rootDir}`)
    }
    byId.set(verified.id, verified)
  }
  return byId
}

export function verifyOfficialPluginsDir(installedOfficialDir, expectedSourceDistDir) {
  const installedMap = verifyBuiltPluginMap(installedOfficialDir, 'Installed official plugins directory')

  if (expectedSourceDistDir) {
    const expectedMap = verifyBuiltPluginMap(expectedSourceDistDir, 'Expected official plugins dist directory')
    for (const [id, expected] of expectedMap.entries()) {
      const actual = installedMap.get(id)
      if (!actual) {
        throw new Error(`Official plugin "${id}" is missing from installed directory ${installedOfficialDir}`)
      }
      if (actual.entrySha256 !== expected.entrySha256) {
        throw new Error(
          `Official plugin "${id}" entry SHA-256 mismatch: installed=${actual.entrySha256}, expected=${expected.entrySha256}`,
        )
      }
    }
    for (const id of installedMap.keys()) {
      if (!expectedMap.has(id)) {
        throw new Error(
          `Unexpected stale official plugin "${id}" found in installed directory ${installedOfficialDir}`,
        )
      }
    }
  }

  const plugins = [...installedMap.values()]
    .sort((a, b) => a.id.localeCompare(b.id))
    .map((item) => ({
      id: item.id,
      version: item.version,
      entry: item.entry,
      entrySize: item.entrySize,
      entrySha256: item.entrySha256,
      stylesCount: item.styles.length,
    }))

  return {
    dir: installedOfficialDir,
    expectedDir: expectedSourceDistDir || null,
    count: plugins.length,
    plugins,
  }
}

export function verifyLocalRelease({
  installDir,
  expectDesktopSha,
  expectAppSha,
  expectDaemonPid,
  expectOfficialPluginsDir,
  skipFrontend = false,
  skipDesktopWindow = false,
} = {}) {
  const env = resolveInstalledEnv(installDir)
  const report = {
    ok: false,
    installDir: env.installDirWin,
    stateDir: env.stateDirWin,
    gates: {},
  }

  // Gate A: Browser/PWA frontend dist integrity
  if (!skipFrontend) {
    const distDir = path.join(env.installDirHost, 'server', 'frontend', 'dist')
    report.gates.frontend = verifyFrontendDist(distDir, { mode: 'browser' })
  }

  // Gate B: Installed desktop binary integrity + SHA
  const desktopHost = path.join(env.installDirHost, 'Astrorder.exe')
  const desktopFile = assertPackagedBinaryCandidate(desktopHost)
  if (expectDesktopSha && desktopFile.sha256.toLowerCase() !== expectDesktopSha.toLowerCase()) {
    throw new Error(`Installed Astrorder.exe SHA-256 mismatch: got ${desktopFile.sha256}, expected ${expectDesktopSha}`)
  }

  // Optional App binary SHA check
  const appHost = path.join(env.installDirHost, 'server', 'bin', 'astrorder-app.exe')
  const appFile = assertPeExecutable(appHost, 'Installed astrorder-app.exe')
  if (expectAppSha && appFile.sha256.toLowerCase() !== expectAppSha.toLowerCase()) {
    throw new Error(`Installed astrorder-app.exe SHA-256 mismatch: got ${appFile.sha256}, expected ${expectAppSha}`)
  }

  // Query live Windows checks in a single PowerShell round-trip
  const live = queryLiveInstallation(env.installDirWin, env.stateDirWin)

  report.gates.desktopBinary = {
    sha256: desktopFile.sha256,
    check: assertDesktopCheckOutput(live.desktopCheck, {
      expectedStateDir: env.stateDirWin,
      requirePackaged: true,
    }),
  }

  // Gate C: Desktop interactive process & visible window
  const desktopProc = assertDesktopProcess(live.desktopProcesses, {
    expectedExePath: `${env.installDirWin}\\Astrorder.exe`,
    requireWindow: !skipDesktopWindow,
  })
  report.gates.desktopProcess = desktopProc

  // Gate D: App server healthy via astrorderctl
  const appStatus = assertAppServiceStatus(live.appStatus, {
    expectedExecutable: `${env.installDirWin}\\server\\bin\\astrorder-app.exe`,
  })
  report.gates.appService = {
    pid: appStatus.process?.pid,
    address: appStatus.address,
    healthy: appStatus.healthy,
    sha256: appFile.sha256,
  }

  // Gate E: Session daemon running & untouched
  const daemonStatus = assertDaemonServiceStatus(live.daemonStatus, {
    expectedPid: expectDaemonPid,
  })
  report.gates.daemonService = {
    pid: daemonStatus.process?.pid,
    port: daemonStatus.port,
    sessionCount: daemonStatus.session_count,
  }

  // Gate F (Gate 6): Bundled official ESM plugins integrity & parity
  const installedOfficialDir = path.join(env.installDirHost, 'server', 'plugins', 'official')
  const defaultOfficialDist = path.join(REPO_ROOT, 'plugins', 'official-dist')
  const resolvedExpectedOfficialDir =
    expectOfficialPluginsDir || (existsSync(defaultOfficialDist) ? defaultOfficialDist : undefined)
  report.gates.officialPlugins = verifyOfficialPluginsDir(installedOfficialDir, resolvedExpectedOfficialDir)

  report.ok = true
  return report
}

function parseArgs(argv) {
  const opts = {}
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--check-desktop-exe') opts.checkDesktopExe = argv[++i]
    else if (arg === '--install-dir') opts.installDir = argv[++i]
    else if (arg === '--expect-desktop-sha') opts.expectDesktopSha = argv[++i]
    else if (arg === '--expect-app-sha') opts.expectAppSha = argv[++i]
    else if (arg === '--expect-daemon-pid') opts.expectDaemonPid = Number(argv[++i])
    else if (arg === '--expect-official-plugins-dir') opts.expectOfficialPluginsDir = path.resolve(argv[++i] || '')
    else if (arg === '--expected-state-dir') opts.expectedStateDir = argv[++i]
    else if (arg === '--skip-frontend') opts.skipFrontend = true
    else if (arg === '--skip-desktop-window') opts.skipDesktopWindow = true
    else if (arg === '--allow-uninstalled') opts.allowUninstalled = true
    else throw new Error(`Unknown argument: ${arg}`)
  }
  return opts
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const opts = parseArgs(process.argv.slice(2))
    if (opts.checkDesktopExe) {
      const result = verifyDesktopCandidate(opts.checkDesktopExe, {
        expectedStateDir: opts.expectedStateDir,
        allowUninstalled: Boolean(opts.allowUninstalled),
      })
      console.log(JSON.stringify({ ok: true, mode: 'check-desktop-exe', ...result }))
    } else {
      const report = verifyLocalRelease(opts)
      console.log(JSON.stringify(report))
    }
  } catch (err) {
    console.error(`[release-gate] FAILED: ${err.message}`)
    process.exit(1)
  }
}
