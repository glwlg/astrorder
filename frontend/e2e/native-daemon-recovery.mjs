// Opt-in real SSH Codex + Go daemon + normal App Server + Chromium acceptance.
import { chromium, expect } from '@playwright/test'
import { spawn, execFileSync } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import { once } from 'node:events'
import fs from 'node:fs/promises'
import net from 'node:net'
import path from 'node:path'

if (process.env.ASTRORDER_REAL_INFERENCE !== '1') throw new Error('Explicit inference opt-in required')
const root = path.resolve('..')
const binary = process.env.ASTRORDER_GO_SESSIOND
// Model/tool round trips have a separate budget from transport/UI latency.
const inferenceTimeout = Number(process.env.ASTRORDER_E2E_INFERENCE_TIMEOUT_MS || 600000)
if (!Number.isFinite(inferenceTimeout) || inferenceTimeout < 1000 || inferenceTimeout > 1200000) throw new Error('Invalid inference timeout')
const remote = JSON.parse(process.env.ASTRORDER_REAL_SSH_SETTINGS || '{}')
if (!binary || !remote.host || !remote.user) throw new Error('Isolated binary and explicit SSH settings required')
const scratch = process.env.ASTRORDER_AUDIT_SCRATCH || path.join(process.env.LOCALAPPDATA, 'hermes/cache/scratch')
if (!scratch) throw new Error('Scratch directory required')
const work = await fs.mkdtemp(path.join(scratch, 'astrorder-browser-native-'))
const ssh = ['-o', 'BatchMode=yes', '-o', 'ConnectTimeout=5', '-p', String(remote.port || 22), `${remote.user}@${remote.host}`]
const remoteRun = command => execFileSync('ssh', [...ssh, command], { encoding: 'utf8', timeout: 20000 }).trim()
const cwd = remoteRun('mktemp -d /home/luwei/astrorder-browser-audit-XXXXXX')
if (!/^\/home\/luwei\/astrorder-browser-audit-[a-zA-Z0-9]+$/.test(cwd)) throw new Error('Unexpected isolated workspace')
const freePort = async () => { const server = net.createServer(); server.listen(0, '127.0.0.1'); await once(server, 'listening'); const port = server.address().port; await new Promise(resolve => server.close(resolve)); return port }
const appPort = await freePort(), daemonPort = await freePort()
const origin = `http://127.0.0.1:${appPort}`
const daemonSecret = randomBytes(32).toString('hex'), browserSecret = randomBytes(32).toString('hex')
const baseEnv = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('ASTRORDER_')))
const config = path.join(work, 'runtime.json')
await fs.writeFile(config, JSON.stringify({ runtimes: [] }))
const appEnv = { ...baseEnv, PYTHONIOENCODING: 'utf-8', ASTRORDER_PORT: String(appPort),
  ASTRORDER_DATABASE_URL: `sqlite:///${path.join(work, 'app.db').replaceAll('\\', '/')}`,
  ASTRORDER_ATTACHMENTS_DIR: path.join(work, 'attachments'), ASTRORDER_BROWSER_SECRET: browserSecret,
  ASTRORDER_CONNECTOR_SECRET: randomBytes(32).toString('hex'),
  ASTRORDER_STATIC_DIR: process.env.ASTRORDER_E2E_STATIC_DIR || path.join(root, 'frontend/dist'),
  ASTRORDER_ALLOWED_ORIGINS: origin, ASTRORDER_ENABLE_LAUNCH: '0',
  ASTRORDER_AUTO_CONNECT_LOCAL_HERMES: '1', ASTRORDER_HERMES_EXECUTABLE: path.join(work, 'no-hermes.exe'),
  ASTRORDER_SESSION_DAEMON_ENABLED: '1', ASTRORDER_DAEMON_CODEX_ENABLED: '1',
  ASTRORDER_SESSION_DAEMON_ENDPOINT: `ws://127.0.0.1:${daemonPort}`, ASTRORDER_SESSION_DAEMON_SECRET: daemonSecret,
  ASTRORDER_SESSION_DAEMON_REQUEST_TIMEOUT: '60' }
let app, daemon, browser, context, sid, agentId, connectionId
const results = []
const logHandles = []
async function start(executable, args, env, label) {
  const log = await fs.open(path.join(work, label + '.log'), 'a'); logHandles.push(log)
  return spawn(executable, args, { cwd: path.join(root, 'backend'), env, windowsHide: true, stdio: ['ignore', log.fd, log.fd] })
}
async function stop(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return
  const exited = once(child, 'exit'); child.kill(); await exited
}
async function startApp() {
  app = await start(path.join(root, 'backend/.venv/Scripts/python.exe'), ['-m', 'uvicorn', 'astrorder.main:app', '--host', '127.0.0.1', '--port', String(appPort)], appEnv, 'app')
  await expect.poll(async () => { if (app.exitCode !== null) throw new Error('Isolated App exited'); try { return (await fetch(origin + '/health')).status } catch { return 0 } }, { timeout: 60000 }).toBe(200)
}
async function control(action, fields = {}) {
  const socket = new WebSocket(`ws://127.0.0.1:${daemonPort}`)
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject })
  const request = (action, fields) => new Promise((resolve, reject) => {
    const request_id = randomBytes(12).toString('hex')
    const timer = setTimeout(() => reject(new Error('Daemon control timeout: ' + action)), 65000)
    socket.onmessage = event => { const response = JSON.parse(event.data); if (response.request_id !== request_id) return; clearTimeout(timer); if (response.action === 'error') reject(new Error(response.detail)); else resolve(response) }
    socket.send(JSON.stringify({ action, request_id, ...fields }))
  })
  try { await request('daemon.handshake', { secret: daemonSecret }); return await request(action, fields) } finally { socket.close() }
}
async function api(method, endpoint, data) {
  const response = await context.request.fetch(origin + endpoint, { method, data, timeout: 120000 })
  if (!response.ok()) throw new Error(`${method} ${endpoint}: ${response.status()} ${(await response.text()).slice(0, 600)}`)
  return response.json()
}
async function record(name, run) {
  await run(); results.push({ name, status: 'passed' }); await fs.writeFile(path.join(work, 'results.json'), JSON.stringify(results, null, 2)); console.log('PASS ' + name)
}
try {
  daemon = await start(binary, [], { ...baseEnv, ASTRORDER_SESSION_DAEMON_PORT: String(daemonPort), ASTRORDER_SESSION_DAEMON_SECRET: daemonSecret, ASTRORDER_SESSION_DAEMON_DB: path.join(work, 'journal.db'), ASTRORDER_SESSION_DAEMON_CONFIG: config }, 'daemon')
  await expect.poll(async () => { try { return (await control('daemon.status')).daemon_id || '' } catch { return '' } }, { timeout: 15000 }).not.toBe('')
  await startApp()
  browser = await chromium.launch({ executablePath: process.env.ASTRORDER_CHROME || 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await api('POST', '/api/v1/auth/session', { token: browserSecret })
  const connection = await api('PUT', '/api/v1/connections/ssh', { host: remote.host, user: remote.user, port: remote.port || 22, workspace: cwd, display_name: 'Isolated native browser audit' })
  connectionId = connection.id; agentId = 'ssh-codex-' + connectionId
  await api('POST', `/api/v1/environments/${connectionId}/agents/codex/connect`)
  const session = await api('POST', '/api/v1/sessions', { agent_id: agentId, workspace: cwd, title: 'Isolated native browser recovery' })
  sid = session.id
  if (!sid) throw new Error('Create returned no session identity')
  const page = await context.newPage()
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  await page.goto(`${origin}/chat/${sid}?agent_id=${agentId}`)
  await expect(page.getByLabel('消息内容')).toBeEnabled({ timeout: 60000 })
  const documentId = randomBytes(16).toString('hex')
  await page.evaluate(id => { window.__auditDocumentId = id }, documentId)
  const before = await control('daemon.status')
  const firstAppPid = app.pid, daemonPid = daemon.pid
  const proof = randomBytes(12).toString('hex')
  const prompt = `Use the terminal tool to run a command that waits 12 seconds, then writes exactly ${proof} with no newline to native-proof.txt in the current directory and reads it back. Only modify this file. Then reply with exactly VERIFIED:${proof}.`
  await record('browser sends through normal App into native Go-owned Codex', async () => {
    await page.getByLabel('消息内容').fill(prompt)
    await page.getByRole('button', { name: '发送', exact: true }).click()
    await expect.poll(async () => (await control('daemon.status')).sessions[sid]?.status, { timeout: 30000 }).toBe('running')
  })
  await record('native work completes while isolated App process is stopped', async () => {
    await stop(app)
    await expect.poll(async () => (await control('daemon.status')).sessions[sid]?.status, { timeout: inferenceTimeout }).toBe('idle')
    expect(remoteRun(`cat ${cwd}/native-proof.txt`)).toBe(proof)
    expect(daemon.exitCode).toBe(null)
    expect((await control('daemon.status')).daemon_id).toBe(before.daemon_id)
  })
  await record('same browser document restores reply after App-only restart', async () => {
    await startApp()
    expect(app.pid).not.toBe(firstAppPid); expect(daemon.pid).toBe(daemonPid)
    await expect(page.locator('.message-assistant').filter({ hasText: `VERIFIED:${proof}` })).toHaveCount(1, { timeout: 120000 })
    expect(await page.evaluate(() => window.__auditDocumentId)).toBe(documentId)
    await expect(page.locator('.transcript-error')).toHaveCount(0, { timeout: 30000 })
    await expect(page.locator('.message-user')).toHaveCount(1, { timeout: 30000 })
    const messages = await api('GET', `/api/v1/sessions/${sid}/messages?agent_id=${agentId}&limit=200`)
    expect(messages.items.some(message => message.role === 'tool')).toBe(true)
    expect(messages.items.filter(message => message.role === 'assistant' && message.kind === 'message' && message.text.includes(`VERIFIED:${proof}`)).map(message => message.id)).toHaveLength(1)
    expect((await control('daemon.status')).daemon_id).toBe(before.daemon_id)
    await page.screenshot({ path: path.join(work, 'recovered.png'), fullPage: true })
    expect(errors).toEqual([])
  })
  await record('real native approval round trip in the browser', async () => {
    await api('POST', `/api/v1/sessions/${sid}/approval-mode`, { agent_id: agentId, mode: 'manual' })
    const approvedProof = randomBytes(12).toString('hex')
    await page.getByLabel('消息内容').fill(`Use the terminal tool to run python3 to write exactly ${approvedProof} with no newline to approval-proof.txt in the current directory, then read that file back. Request approval if needed. Modify only that file. Reply exactly APPROVED:${approvedProof}.`)
    await page.getByRole('button', { name: '发送', exact: true }).click()
    await expect.poll(async () => (await control('daemon.status')).sessions[sid]?.status, { timeout: 300000 }).toBe('waiting_approval')
    await expect(page.locator('.approval-approve').first()).toBeVisible({ timeout: 90000 })
    await stop(app)
    expect((await control('daemon.status')).sessions[sid]?.status).toBe('waiting_approval')
    await startApp()
    await expect(page.locator('.approval-approve').first()).toBeVisible({ timeout: 90000 })
    expect((await control('daemon.status')).daemon_id).toBe(before.daemon_id)
    await page.locator('.approval-approve').first().click()
    // A native turn may ask separately for writing and reading the proof file.
    // Keep acting through the visible card in this isolated session.
    await expect.poll(async () => {
      const status = (await control('daemon.status')).sessions[sid]?.status
      if (status === 'waiting_approval') {
        const approve = page.locator('.approval-approve').first()
        if (await approve.isVisible() && await approve.isEnabled()) await approve.click()
      }
      return status
    }, { timeout: inferenceTimeout, intervals: [500, 1000, 2000] }).toBe('idle')
    expect(remoteRun(`cat ${cwd}/approval-proof.txt`)).toBe(approvedProof)
    await expect(page.locator('.message-assistant').filter({ hasText: `APPROVED:${approvedProof}` })).toHaveCount(1, { timeout: 30000 })
    expect(await page.evaluate(() => window.__auditDocumentId)).toBe(documentId)
  })
  await record('native fork preserves parent history and title', async () => {
    const child = await api('POST', `/api/v1/sessions/${sid}/fork`, { agent_id: agentId, worktree: false })
    expect(child.id).not.toBe(sid)
    try {
      const history = await control('session.history_page', { session_id: child.id })
      expect(history.result.thread.id).toBe(child.id)
      expect(history.result.thread.name).toBe(child.title)
      expect(JSON.stringify(history.result.thread.turns)).toContain(`VERIFIED:${proof}`)
    } finally {
      await api('DELETE', `/api/v1/sessions/${child.id}?agent_id=${agentId}`)
      expect((await control('daemon.status')).sessions[child.id]).toBeUndefined()
    }
  })
  await record('native and App session deletion read back', async () => {
    await api('DELETE', `/api/v1/sessions/${sid}?agent_id=${agentId}`)
    expect((await control('daemon.status')).sessions[sid]).toBeUndefined()
    const sessions = await api('GET', `/api/v1/sessions?agent_id=${agentId}`)
    expect(sessions.items.some(row => row.id === sid)).toBe(false)
    sid = null
  })
} catch (error) {
  results.push({ status: 'failed', error: String(error.stack) })
  await fs.writeFile(path.join(work, 'results.json'), JSON.stringify(results, null, 2))
  console.error(String(error.stack))
  process.exitCode = 1
} finally {
  if (sid && daemon?.exitCode === null) {
    try {
      const status = await control('daemon.status')
      if (['running', 'waiting_approval'].includes(status.sessions[sid]?.status)) {
        const history = await control('session.history_page', { session_id: sid })
        const active = history.result?.thread?.turns?.findLast(turn => turn.status === 'inProgress')
        if (!active?.id) throw new Error('Active native turn could not be identified for cleanup')
        await control('session.interrupt', { session_id: sid, turn_id: active.id })
      }
      await expect.poll(async () => (await control('daemon.status')).sessions[sid]?.status, { timeout: 60000 }).toBe('idle')
      await control('session.delete', { session_id: sid })
      expect((await control('daemon.status')).sessions[sid]).toBeUndefined()
    } catch (error) { results.push({ status: 'cleanup_failed', error: String(error) }); process.exitCode = 1 }
  }
  await browser?.close()
  await stop(app)
  if (daemon?.exitCode === null) { try { await control('daemon.shutdown') } catch {} await stop(daemon) }
  for (const handle of logHandles) await handle.close()
  remoteRun(`rm -rf -- ${cwd}; test ! -e ${cwd}`)
  await fs.writeFile(path.join(work, 'results.json'), JSON.stringify(results, null, 2))
  console.log('ARTIFACTS ' + work)
}
