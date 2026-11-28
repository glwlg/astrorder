import { cpSync, existsSync, mkdirSync, readdirSync, renameSync, rmSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { verifyBuiltPluginDist } from './build-official-plugins.mjs'
import { verifyFrontendDist } from './verify-frontend-dist.mjs'
import {
  assertDaemonServiceStatus,
  assertDesktopCheckOutput,
  assertPackagedBinaryCandidate,
  assertPeExecutable,
  psQuote,
  resolveInstalledEnv,
  runDesktopCheckOnWindows,
  runPowerShell,
  sha256File,
  toWindowsPath,
  verifyLocalRelease,
  verifyOfficialPluginsDir,
} from './verify-local-release.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

function parseArgs(argv) {
  const opts = {
    frontend: false,
    desktop: false,
    app: false,
    plugins: false,
    dryRun: false,
    installDir: '',
    desktopExe: path.join(root, 'desktop-wails', 'bin', 'Astrorder.exe'),
    appExe: path.join(root, 'app-server-go', 'bin', 'astrorder-app.exe'),
    frontendDist: path.join(root, 'frontend', 'dist'),
    officialPluginsDist: path.join(root, 'plugins', 'official-dist'),
  }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--frontend') opts.frontend = true
    else if (arg === '--desktop') opts.desktop = true
    else if (arg === '--app') opts.app = true
    else if (arg === '--plugins') opts.plugins = true
    else if (arg === '--all') {
      opts.frontend = true
      opts.desktop = true
      opts.app = true
      opts.plugins = true
    } else if (arg === '--dry-run') opts.dryRun = true
    else if (arg === '--install-dir') opts.installDir = argv[++i] || ''
    else if (arg === '--desktop-exe') opts.desktopExe = path.resolve(argv[++i] || '')
    else if (arg === '--app-exe') opts.appExe = path.resolve(argv[++i] || '')
    else if (arg === '--frontend-dist') opts.frontendDist = path.resolve(argv[++i] || '')
    else if (arg === '--official-plugins-dist') opts.officialPluginsDist = path.resolve(argv[++i] || '')
    else throw new Error(`Unknown argument: ${arg}`)
  }
  if (!opts.frontend && !opts.desktop && !opts.app && !opts.plugins) {
    throw new Error('Usage: node scripts/deploy-local.mjs [--frontend] [--desktop] [--app] [--plugins] [--all] [--install-dir <dir>] [--official-plugins-dist <dir>] [--dry-run]')
  }
  return opts
}

function syncFrontendVerified(srcDist, installDirHost) {
  verifyFrontendDist(srcDist, { mode: 'browser' })
  const frontendRoot = path.join(installDirHost, 'server', 'frontend')
  const staging = path.join(frontendRoot, 'dist.staging')
  const liveDist = path.join(frontendRoot, 'dist')
  const backup = path.join(frontendRoot, 'dist.old')
  rmSync(staging, { recursive: true, force: true })
  mkdirSync(frontendRoot, { recursive: true })
  cpSync(srcDist, staging, { recursive: true })
  verifyFrontendDist(staging, { mode: 'browser' })

  // Atomic swap when unobstructed; fall back to verified in-place copy if Windows holds a directory handle.
  rmSync(backup, { recursive: true, force: true })
  let swapped = false
  const liveDistWin = toWindowsPath(liveDist)
  const stagingWin = toWindowsPath(staging)
  const backupWin = toWindowsPath(backup)
  try {
    if (existsSync(liveDist)) {
      runPowerShell(`Move-Item -Force ${psQuote(liveDistWin)} ${psQuote(backupWin)}`)
    }
    runPowerShell(`Move-Item -Force ${psQuote(stagingWin)} ${psQuote(liveDistWin)}`)
    rmSync(backup, { recursive: true, force: true })
    swapped = true
  } catch {
    if (!existsSync(liveDist) && existsSync(backup)) {
      try {
        runPowerShell(`Move-Item -Force ${psQuote(backupWin)} ${psQuote(liveDistWin)}`)
      } catch {
        // Restore best-effort before falling through
      }
    }
  }
  if (!swapped) {
    mkdirSync(liveDist, { recursive: true })
    cpSync(staging, liveDist, { recursive: true, force: true })
    rmSync(staging, { recursive: true, force: true })
    rmSync(backup, { recursive: true, force: true })
  }
  return verifyFrontendDist(liveDist, { mode: 'browser' })
}

export function syncOfficialPluginsVerified(srcDir, installDirHost) {
  if (!srcDir || !existsSync(srcDir) || !statSync(srcDir).isDirectory()) {
    throw new Error(`Official plugins dist directory does not exist: ${srcDir}`)
  }
  const pluginDirs = readdirSync(srcDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && !entry.name.startsWith('.'))
    .map((entry) => path.join(srcDir, entry.name))
    .filter((dir) => existsSync(path.join(dir, 'plugin.json')))
    .sort()
  if (pluginDirs.length === 0) {
    throw new Error(`No official plugins found in ${srcDir}`)
  }

  const expectedById = new Map()
  for (const pluginDir of pluginDirs) {
    const verified = verifyBuiltPluginDist(pluginDir)
    if (expectedById.has(verified.id)) {
      throw new Error(`Duplicate official plugin id "${verified.id}" in ${srcDir}`)
    }
    expectedById.set(verified.id, { srcPluginDir: pluginDir, verified })
  }

  const pluginsRoot = path.join(installDirHost, 'server', 'plugins')
  const officialRoot = path.join(pluginsRoot, 'official')
  const stagingRoot = path.join(pluginsRoot, '.official.staging')
  const backupRoot = path.join(pluginsRoot, '.official.old')

  mkdirSync(pluginsRoot, { recursive: true })
  rmSync(stagingRoot, { recursive: true, force: true })
  mkdirSync(stagingRoot, { recursive: true })

  for (const [id, { srcPluginDir, verified }] of expectedById.entries()) {
    const stagedPluginDir = path.join(stagingRoot, id)
    cpSync(srcPluginDir, stagedPluginDir, { recursive: true })
    const stagedVerified = verifyBuiltPluginDist(stagedPluginDir)
    if (stagedVerified.entrySha256 !== verified.entrySha256) {
      rmSync(stagingRoot, { recursive: true, force: true })
      throw new Error(
        `Staged official plugin "${id}" SHA-256 mismatch: got ${stagedVerified.entrySha256}, expected ${verified.entrySha256}`,
      )
    }
  }

  // Atomic swap when unobstructed; preserve server/plugins/external/ completely untouched.
  rmSync(backupRoot, { recursive: true, force: true })
  let swapped = false
  try {
    if (existsSync(officialRoot)) {
      renameSync(officialRoot, backupRoot)
    }
    renameSync(stagingRoot, officialRoot)
    rmSync(backupRoot, { recursive: true, force: true })
    swapped = true
  } catch {
    if (!existsSync(officialRoot) && existsSync(backupRoot)) {
      try {
        renameSync(backupRoot, officialRoot)
      } catch {
        // Restore best-effort before falling through to in-place verified copy.
      }
    }
  }

  if (!swapped) {
    mkdirSync(officialRoot, { recursive: true })
    for (const entry of readdirSync(officialRoot, { withFileTypes: true })) {
      if (!expectedById.has(entry.name)) {
        rmSync(path.join(officialRoot, entry.name), { recursive: true, force: true })
      }
    }
    for (const id of expectedById.keys()) {
      const targetPluginDir = path.join(officialRoot, id)
      rmSync(targetPluginDir, { recursive: true, force: true })
      cpSync(path.join(stagingRoot, id), targetPluginDir, { recursive: true, force: true })
    }
    rmSync(stagingRoot, { recursive: true, force: true })
    rmSync(backupRoot, { recursive: true, force: true })
  }

  return verifyOfficialPluginsDir(officialRoot, srcDir)
}

export function runDeploy(argv) {
  const opts = parseArgs(argv)
  const plan = { dryRun: opts.dryRun, components: [] }

  let desktopCandidate = null
  let appCandidate = null
  let officialPluginsCheck = null
  if (opts.frontend) {
    verifyFrontendDist(opts.frontendDist, { mode: 'browser' })
    plan.components.push('frontend')
  }
  if (opts.plugins) {
    officialPluginsCheck = verifyOfficialPluginsDir(opts.officialPluginsDist)
    plan.components.push('plugins')
  }
  if (opts.app) {
    appCandidate = assertPeExecutable(opts.appExe, 'Candidate astrorder-app.exe')
    plan.components.push('app')
  }
  if (opts.desktop) {
    desktopCandidate = assertPackagedBinaryCandidate(opts.desktopExe)
    plan.components.push('desktop')
  }
  if (opts.dryRun) {
    return {
      ok: true,
      dryRun: true,
      components: plan.components,
      desktopSha: desktopCandidate?.sha256,
      appSha: appCandidate?.sha256,
      officialPluginsCount: officialPluginsCheck?.count,
    }
  }

  const env = resolveInstalledEnv(opts.installDir)
  const serverRootWin = `${env.installDirWin}\\server`
  const ctlWin = `${serverRootWin}\\bin\\astrorderctl.exe`

  // Capture session daemon PID BEFORE touching anything so post-deploy gate proves daemon was untouched.
  const preDaemonRaw = runPowerShell(
    `(& ${psQuote(ctlWin)} service daemon status --root ${psQuote(serverRootWin)} --state-dir ${psQuote(env.stateDirWin)} | Out-String).Trim()`,
  )
  const preDaemon = assertDaemonServiceStatus(preDaemonRaw)
  const preDaemonPid = Number(preDaemon.process.pid)

  // 1. Frontend sync (browser/PWA verified)
  if (opts.frontend) {
    syncFrontendVerified(opts.frontendDist, env.installDirHost)
  }

  // 2. Official ESM plugins sync BEFORE restarting astrorder-app.exe so the restarted app discovers updates immediately
  if (opts.plugins) {
    syncOfficialPluginsVerified(opts.officialPluginsDist, env.installDirHost)
  }

  // 3. App server update via astrorderctl ONLY (never raw kill/start)
  if (opts.app && appCandidate) {
    const installedAppHost = path.join(env.installDirHost, 'server', 'bin', 'astrorder-app.exe')
    runPowerShell(
      `(& ${psQuote(ctlWin)} service app stop --root ${psQuote(serverRootWin)} --state-dir ${psQuote(env.stateDirWin)} | Out-String).Trim()`,
    )
    cpSync(opts.appExe, installedAppHost, { force: true })
    const actualSha = sha256File(installedAppHost)
    if (actualSha !== appCandidate.sha256) {
      throw new Error(`Copied astrorder-app.exe SHA-256 mismatch: got ${actualSha}, expected ${appCandidate.sha256}`)
    }
    runPowerShell(
      `(& ${psQuote(ctlWin)} service app start --root ${psQuote(serverRootWin)} --state-dir ${psQuote(env.stateDirWin)} | Out-String).Trim()`,
    )
  }

  // 4. Packaged desktop update with pre-swap --check gate
  if (opts.desktop && desktopCandidate) {
    const stagedDesktopHost = path.join(env.installDirHost, 'Astrorder.new.exe')
    const stagedDesktopWin = `${env.installDirWin}\\Astrorder.new.exe`
    const installedDesktopWin = `${env.installDirWin}\\Astrorder.exe`

    cpSync(opts.desktopExe, stagedDesktopHost, { force: true })
    try {
      const checkRaw = runDesktopCheckOnWindows(stagedDesktopHost)
      assertDesktopCheckOutput(checkRaw, {
        expectedStateDir: env.stateDirWin,
        requirePackaged: true,
      })
    } catch (err) {
      rmSync(stagedDesktopHost, { force: true })
      throw new Error(`Pre-deploy desktop gate aborted before stopping running client: ${err.message}`)
    }

    runPowerShell(`
      $ErrorActionPreference = 'Stop'
      $targetExe = ${psQuote(installedDesktopWin)}
      $stagedExe = ${psQuote(stagedDesktopWin)}
      $procs = @(Get-Process -Name Astrorder -ErrorAction SilentlyContinue | Where-Object {
        try { $_.Path -and ($_.Path.ToLowerInvariant() -eq $targetExe.ToLowerInvariant()) } catch { $false }
      })
      foreach ($p in $procs) {
        try { Stop-Process -Id $p.Id -Force -ErrorAction Stop } catch {}
      }
      for ($i = 0; $i -lt 30; $i++) {
        $alive = @(Get-Process -Name Astrorder -ErrorAction SilentlyContinue | Where-Object {
          try { $_.Path -and ($_.Path.ToLowerInvariant() -eq $targetExe.ToLowerInvariant()) } catch { $false }
        })
        if ($alive.Count -eq 0) { break }
        Start-Sleep -Milliseconds 250
      }
      $moved = $false
      $lastMoveErr = $null
      for ($i = 0; $i -lt 20; $i++) {
        try {
          Move-Item -LiteralPath $stagedExe -Destination $targetExe -Force -ErrorAction Stop
          $moved = $true
          break
        } catch {
          $lastMoveErr = $_
          Start-Sleep -Milliseconds 250
        }
      }
      if (-not $moved) {
        throw ("Failed to replace " + $targetExe + " with " + $stagedExe + ": " + $lastMoveErr)
      }
      Start-Process -FilePath $targetExe -ArgumentList '--packaged' -WorkingDirectory ${psQuote(env.installDirWin)}
      Start-Sleep -Seconds 3
    `)
  }

  // 5. Mandatory end-to-end post-deploy gate (retries briefly while packaged client reconciles startup)
  let lastErr = null
  for (let attempt = 1; attempt <= 4; attempt++) {
    try {
      return verifyLocalRelease({
        installDir: env.installDirWin,
        expectDesktopSha: desktopCandidate?.sha256,
        expectAppSha: appCandidate?.sha256,
        expectDaemonPid: preDaemonPid,
        expectOfficialPluginsDir: opts.plugins ? opts.officialPluginsDist : undefined,
      })
    } catch (err) {
      lastErr = err
      if (attempt < 4) {
        runPowerShell('Start-Sleep -Seconds 2')
      }
    }
  }
  throw lastErr
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const result = runDeploy(process.argv.slice(2))
    console.log(JSON.stringify(result, null, 2))
  } catch (err) {
    console.error(`[deploy-local] FAILED: ${err.message}`)
    process.exit(1)
  }
}
