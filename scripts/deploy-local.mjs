import { cpSync, existsSync, mkdirSync, rmSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
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
} from './verify-local-release.mjs'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

function parseArgs(argv) {
  const opts = {
    frontend: false,
    desktop: false,
    app: false,
    dryRun: false,
    installDir: '',
    desktopExe: path.join(root, 'desktop-wails', 'bin', 'Astrorder.exe'),
    appExe: path.join(root, 'app-server-go', 'bin', 'astrorder-app.exe'),
    frontendDist: path.join(root, 'frontend', 'dist'),
  }
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i]
    if (arg === '--frontend') opts.frontend = true
    else if (arg === '--desktop') opts.desktop = true
    else if (arg === '--app') opts.app = true
    else if (arg === '--all') {
      opts.frontend = true
      opts.desktop = true
      opts.app = true
    } else if (arg === '--dry-run') opts.dryRun = true
    else if (arg === '--install-dir') opts.installDir = argv[++i] || ''
    else if (arg === '--desktop-exe') opts.desktopExe = path.resolve(argv[++i] || '')
    else if (arg === '--app-exe') opts.appExe = path.resolve(argv[++i] || '')
    else if (arg === '--frontend-dist') opts.frontendDist = path.resolve(argv[++i] || '')
    else throw new Error(`Unknown argument: ${arg}`)
  }
  if (!opts.frontend && !opts.desktop && !opts.app) {
    throw new Error('Usage: node scripts/deploy-local.mjs [--frontend] [--desktop] [--app] [--all] [--install-dir <dir>] [--dry-run]')
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

export function runDeploy(argv) {
  const opts = parseArgs(argv)
  const plan = { dryRun: opts.dryRun, components: [] }

  let desktopCandidate = null
  let appCandidate = null
  if (opts.frontend) {
    verifyFrontendDist(opts.frontendDist, { mode: 'browser' })
    plan.components.push('frontend')
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

  // 2. App server update via astrorderctl ONLY (never raw kill/start)
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

  // 3. Packaged desktop update with pre-swap --check gate
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
      $targetExe = ${psQuote(installedDesktopWin)}
      Get-Process -Name Astrorder -ErrorAction SilentlyContinue | Where-Object {
        try { $_.Path -and ($_.Path.ToLowerInvariant() -eq $targetExe.ToLowerInvariant()) } catch { $false }
      } | Stop-Process -Force -ErrorAction SilentlyContinue
      Start-Sleep -Milliseconds 700
      Move-Item -Force ${psQuote(stagedDesktopWin)} $targetExe
      Start-Process -FilePath $targetExe -ArgumentList '--packaged' -WorkingDirectory ${psQuote(env.installDirWin)}
      Start-Sleep -Seconds 3
    `)
  }

  // 4. Mandatory end-to-end post-deploy gate (retries briefly while packaged client reconciles startup)
  let lastErr = null
  for (let attempt = 1; attempt <= 4; attempt++) {
    try {
      return verifyLocalRelease({
        installDir: env.installDirWin,
        expectDesktopSha: desktopCandidate?.sha256,
        expectAppSha: appCandidate?.sha256,
        expectDaemonPid: preDaemonPid,
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
