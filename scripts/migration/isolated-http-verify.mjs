import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import path from 'node:path'
import net from 'node:net'
import { spawn } from 'node:child_process'

const [rootArg, scratchArg, stageArg] = process.argv.slice(2)
assert(rootArg && scratchArg, 'usage: node isolated-http-verify.mjs <root> <scratch> [binary-directory]')
const root = path.resolve(rootArg)
const stage = path.resolve(stageArg || path.join(root, 'desktop/.runtime/go-stage'))
const packagedStatic = path.resolve(stage, '..', 'frontend', 'dist')
const staticDir = stageArg ? packagedStatic : path.join(root, 'frontend', 'dist')
await fs.access(path.join(staticDir, 'index.html'))
const state = await fs.mkdtemp(path.join(path.resolve(scratchArg), 'closure-native-http-'))
const suffix = process.platform === 'win32' ? '.exe' : ''
const children = []
const assertions = []
const pause = ms => new Promise(resolve => setTimeout(resolve, ms))
async function freePort() {
  const server = net.createServer()
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
  const port = server.address().port
  await new Promise(resolve => server.close(resolve))
  return port
}
const dport = await freePort()
let aport = await freePort()
while (aport === dport) aport = await freePort()
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.toUpperCase().startsWith('ASTRORDER_')))
Object.assign(env, {
  ASTRORDER_BROWSER_SECRET: 'isolated-browser', ASTRORDER_CONNECTOR_SECRET: 'isolated-connector',
  ASTRORDER_SESSION_DAEMON_SECRET: 'isolated-daemon', ASTRORDER_SESSION_DAEMON_GRPC_CREDENTIAL: 'isolated-grpc',
  ASTRORDER_SESSION_DAEMON_GRPC_TARGET: `127.0.0.1:${dport}`, ASTRORDER_SESSION_DAEMON_DB: path.join(state, 'daemon.sqlite3'),
  ASTRORDER_SESSION_DAEMON_WS_DISABLED: '1', ASTRORDER_DB_PATH: path.join(state, 'app.sqlite3'),
})
async function start(name, args) {
  const log = await fs.open(path.join(state, `${name}.log`), 'w')
  const child = spawn(path.join(stage, name + suffix), args, { cwd: state, env, windowsHide: true, stdio: ['ignore', log.fd, log.fd] })
  const entry = { child, log, error: null, exited: false }
  entry.exit = new Promise(resolve => {
    child.once('error', error => { entry.error = error; entry.exited = true; resolve() })
    child.once('exit', () => { entry.exited = true; resolve() })
  })
  children.push(entry)
  return entry
}
async function until(check, label, timeout = 15000) {
  const deadline = Date.now() + timeout
  while (Date.now() < deadline) {
    for (const entry of children) {
      if (entry.error) throw entry.error
      if (entry.exited) throw new Error(`service exited before ${label}; logs: ${state}`)
    }
    if (await check()) return
    await pause(50)
  }
  throw new Error(`${label} timed out; logs: ${state}`)
}
async function listening(port) {
  return new Promise(resolve => {
    const socket = net.connect({ host: '127.0.0.1', port })
    socket.setTimeout(250)
    const done = ok => { socket.destroy(); resolve(ok) }
    socket.once('connect', () => done(true)); socket.once('error', () => done(false)); socket.once('timeout', () => done(false))
  })
}
async function request(method, route, body, authenticated = true) {
  const headers = { 'Content-Type': 'application/json' }
  if (authenticated) headers.Authorization = 'Bearer isolated-browser'
  const response = await fetch(`http://127.0.0.1:${aport}${route}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal: AbortSignal.timeout(10000) })
  return { status: response.status, body: await response.text() }
}
try {
  const daemon = await start('astrorder-sessiond', [])
  await until(() => listening(dport), 'daemon listener')
  const app = await start('astrorder-app', ['--listen', `127.0.0.1:${aport}`, '--static-dir', staticDir])
  await until(async () => { try { return (await request('GET', '/health')).status === 200 } catch { return false } }, 'App health')
  const cases = [
    ['GET', '/health', undefined, 200, body => assert.equal(JSON.parse(body).service, 'astrorder')],
    ['GET', '/chat', undefined, 200, body => assert.match(body, /<html/i)],
    ['GET', '/openapi.json', undefined, 200, body => assert.ok(JSON.parse(body).paths['/api/v1/sessions'])],
    ['GET', '/docs', undefined, 200, body => assert.match(body, /html/i)],
    ['POST', '/api/v1/tools/jev-filter', { output: 'short fixture output' }, 200, body => assert.equal(JSON.parse(body).filtered, 'short fixture output')],
    ['POST', '/api/v1/runtime/launch', { kind: 'codex', workspace: state }, 409],
    ['POST', '/api/v1/services/daemon/restart', { confirm_active: false }, 422],
    ['GET', '/api/v1/services/daemon/restart/missing', undefined, 404],
    ['GET', '/api/v1/legacy/1', undefined, 404],
  ]
  for (const [method, route, body, expected, validate] of cases) {
    const response = await request(method, route, body)
    assert.equal(response.status, expected, route)
    validate?.(response.body)
    assertions.push({ method, route, status: response.status })
  }
  assert.equal((await request('GET', '/api/v1/preferences', undefined, false)).status, 401)
  assertions.push({ unauthenticatedPreferencesDenied: true })
  assert.equal((await request('POST', '/_desktop/shutdown', {})).status, 200)
  await Promise.race([app.exit, pause(15000)])
  assert.ok(app.exited, 'App did not shut down')
  assert.equal(app.child.exitCode, 0)
  assert.equal(daemon.exited, false)
  assert.equal(await listening(dport), true)
  assertions.push({ appShutdownPreservesDaemon: true })
  await fs.writeFile(path.join(state, 'result.json'), JSON.stringify({ ok: true, assertions }, null, 2))
  console.log(JSON.stringify({ ok: true, assertions, state }))
} finally {
  for (const entry of children.reverse()) {
    if (!entry.exited) entry.child.kill()
    await Promise.race([entry.exit, pause(10000)])
    if (!entry.exited) throw new Error(`isolated process ${entry.child.pid} did not exit`)
    await entry.log.close()
  }
}
