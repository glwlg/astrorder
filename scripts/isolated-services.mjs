import { spawnSync, execFile } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { promisify } from 'node:util'

const execFileAsync = promisify(execFile)

export const ISOLATED_APP_PORT = 30102
export const ISOLATED_DAEMON_GRPC_TARGET = '127.0.0.1:30101'
export const ISOLATED_APP_ORIGIN = `http://127.0.0.1:${ISOLATED_APP_PORT}`

export function getIsolatedPaths(customRoot) {
  const root = customRoot || path.resolve(import.meta.dirname, '..')
  const localAppData = process.env.LOCALAPPDATA || 'C:\\Users\\luwei\\AppData\\Local'
  const stateDir = process.env.ASTRORDER_ISOLATED_STATE_DIR || path.join(localAppData, 'Astrorder-Wails-Dev')
  const production = path.join(localAppData, 'Astrorder')
  const relative = path.relative(production, path.resolve(stateDir))
  if (!relative || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`))) {
    throw new Error('Isolated services must not use production state')
  }
  const dataDir = path.join(stateDir, 'data')
  const binDir = path.join(root, 'bin')
  const ctlExecutable = path.join(binDir, 'astrorderctl.exe')
  const appExecutable = path.join(binDir, 'astrorder-app.exe')
  const daemonExecutable = path.join(binDir, 'astrorder-sessiond.exe')

  return {
    root,
    stateDir,
    dataDir,
    binDir,
    ctlExecutable,
    appExecutable,
    daemonExecutable,
  }
}

export function ensureBinaries(paths) {
  for (const [name, p] of Object.entries({
    ctl: paths.ctlExecutable,
    app: paths.appExecutable,
    daemon: paths.daemonExecutable,
  })) {
    if (!existsSync(p)) {
      throw new Error(`Required isolated binary not found: ${name} (${p}). Run build first.`)
    }
  }
}

export async function runCtl(paths, args) {
  const { stdout, stderr } = await execFileAsync(paths.ctlExecutable, args, {
    cwd: paths.root,
    windowsHide: true,
    timeout: 30000,
  })
  return { stdout: stdout.trim(), stderr: stderr.trim() }
}

export async function provisionIsolatedEnvironment(paths) {
  mkdirSync(paths.stateDir, { recursive: true })
  mkdirSync(paths.dataDir, { recursive: true })

  const prodJsonPath = path.join(paths.stateDir, 'production.json')
  if (!existsSync(prodJsonPath)) {
    // Run provision
    await runCtl(paths, [
      'provision',
      '--root', paths.root,
      '--state-dir', paths.stateDir,
      '--data-dir', paths.dataDir,
      '--app-executable', paths.appExecutable,
      '--grpc-target', ISOLATED_DAEMON_GRPC_TARGET,
    ])
  }

  // Update production.json with isolated settings
  const prodRaw = readFileSync(prodJsonPath, 'utf8')
  const prod = JSON.parse(prodRaw)
  if (!prod.environment) prod.environment = {}

  let modified = false
  if (prod.environment.ASTRORDER_PORT !== String(ISOLATED_APP_PORT)) {
    prod.environment.ASTRORDER_PORT = String(ISOLATED_APP_PORT)
    modified = true
  }
  const allowed = `http://127.0.0.1:${ISOLATED_APP_PORT},http://localhost:${ISOLATED_APP_PORT},http://wails.localhost`
  if (prod.environment.ASTRORDER_ALLOWED_ORIGINS !== allowed) {
    prod.environment.ASTRORDER_ALLOWED_ORIGINS = allowed
    modified = true
  }
  if (prod.environment.ASTRORDER_SESSION_DAEMON_GRPC_TARGET !== ISOLATED_DAEMON_GRPC_TARGET) {
    prod.environment.ASTRORDER_SESSION_DAEMON_GRPC_TARGET = ISOLATED_DAEMON_GRPC_TARGET
    modified = true
  }
  if (prod.environment.ASTRORDER_SESSION_DAEMON_EXECUTABLE !== paths.daemonExecutable) {
    prod.environment.ASTRORDER_SESSION_DAEMON_EXECUTABLE = paths.daemonExecutable
    modified = true
  }
  if (prod.environment.ASTRORDER_APP_EXECUTABLE !== paths.appExecutable) {
    prod.environment.ASTRORDER_APP_EXECUTABLE = paths.appExecutable
    modified = true
  }
  // Development services must not discover the host's native agent sessions.
  const runtimeConfig = path.join(paths.stateDir, 'empty-runtimes.json')
  writeFileSync(runtimeConfig, JSON.stringify({ runtimes: [] }))
  for (const [key, value] of Object.entries({
    ASTRORDER_SESSION_DAEMON_CONFIG: runtimeConfig,
    ASTRORDER_SESSION_DAEMON_DB: path.join(paths.dataDir, 'session-daemon.sqlite3'),
  })) {
    if (prod.environment[key] !== value) {
      prod.environment[key] = value
      modified = true
    }
  }
  const installedStatic = path.join(paths.root, 'frontend', 'dist')
  if (existsSync(installedStatic) && prod.environment.ASTRORDER_STATIC_DIR !== installedStatic) {
    prod.environment.ASTRORDER_STATIC_DIR = installedStatic
    modified = true
  }

  if (modified) {
    writeFileSync(prodJsonPath, JSON.stringify(prod, null, 2) + '\n', 'utf8')
  }

  return prod
}

export async function getServiceStatus(paths, target) {
  try {
    const { stdout } = await runCtl(paths, [
      'service', target, 'status',
      '--root', paths.root,
      '--state-dir', paths.stateDir,
    ])
    return JSON.parse(stdout)
  } catch (err) {
    return { ok: false, error: err.message, state: 'unknown' }
  }
}

export async function runServiceAction(paths, target, action, confirmActive = false) {
  const args = ['service', target, action, '--root', paths.root, '--state-dir', paths.stateDir]
  if (confirmActive) args.push('--confirm-active')
  const { stdout } = await runCtl(paths, args)
  return JSON.parse(stdout)
}

export async function getIsolatedToken(paths) {
  const { stdout } = await runCtl(paths, ['login', '--state-dir', paths.stateDir])
  return stdout.trim()
}

export async function startIsolatedServices(paths = getIsolatedPaths()) {
  ensureBinaries(paths)
  await provisionIsolatedEnvironment(paths)

  // 1. Check & Start Daemon
  let daemonStatus = await getServiceStatus(paths, 'daemon')
  if (daemonStatus.state !== 'running') {
    const startRes = await runServiceAction(paths, 'daemon', 'start')
    if (!startRes.ok) {
      throw new Error(`Failed to start daemon: ${JSON.stringify(startRes)}`)
    }
  }

  // Poll daemon readiness
  let daemonReady = false
  for (let i = 0; i < 30; i++) {
    daemonStatus = await getServiceStatus(paths, 'daemon')
    if (daemonStatus.state === 'running') {
      daemonReady = true
      break
    }
    await new Promise(r => setTimeout(r, 500))
  }
  if (!daemonReady) {
    throw new Error(`Daemon did not reach running state: ${JSON.stringify(daemonStatus)}`)
  }

  // 2. Check & Start App
  let appStatus = await getServiceStatus(paths, 'app')
  if (appStatus.state !== 'running') {
    const startRes = await runServiceAction(paths, 'app', 'start')
    if (!startRes.ok) {
      throw new Error(`Failed to start app: ${JSON.stringify(startRes)}`)
    }
  }

  // Poll App health endpoint
  let appReady = false
  for (let i = 0; i < 30; i++) {
    try {
      const res = await fetch(`${ISOLATED_APP_ORIGIN}/health`)
      if (res.ok) {
        appReady = true
        break
      }
    } catch {}
    await new Promise(r => setTimeout(r, 500))
  }
  if (!appReady) {
    throw new Error(`App server health check failed on ${ISOLATED_APP_ORIGIN}/health`)
  }

  const token = await getIsolatedToken(paths)
  return {
    appOrigin: ISOLATED_APP_ORIGIN,
    daemonTarget: ISOLATED_DAEMON_GRPC_TARGET,
    token,
    daemonStatus,
    appStatus,
  }
}

export async function stopIsolatedServices(paths = getIsolatedPaths()) {
  ensureBinaries(paths)
  const results = {}
  try {
    results.app = await runServiceAction(paths, 'app', 'stop')
  } catch (err) {
    results.app = { ok: false, error: err.message }
  }
  try {
    results.daemon = await runServiceAction(paths, 'daemon', 'stop', false)
  } catch (err) {
    results.daemon = { ok: false, error: err.message }
  }
  return results
}

// CLI entry
if (process.argv[1] && process.argv[1].endsWith('isolated-services.mjs')) {
  const cmd = process.argv[2] || 'status'
  const paths = getIsolatedPaths()
  try {
    if (cmd === 'start') {
      const info = await startIsolatedServices(paths)
      const { token, ...publicInfo } = info
      console.log(JSON.stringify(publicInfo, null, 2))
    } else if (cmd === 'stop') {
      const res = await stopIsolatedServices(paths)
      console.log(JSON.stringify(res, null, 2))
    } else if (cmd === 'status') {
      const [app, daemon] = await Promise.all([
        getServiceStatus(paths, 'app'),
        getServiceStatus(paths, 'daemon'),
      ])
      console.log(JSON.stringify({ app, daemon }, null, 2))
    } else if (cmd === 'token') {
      const token = await getIsolatedToken(paths)
      console.log(token)
    } else {
      console.error(`Unknown command: ${cmd}. Use start, stop, status, token`)
      process.exit(1)
    }
  } catch (err) {
    console.error(err)
    process.exit(1)
  }
}
