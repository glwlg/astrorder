// Manual native read-only UI probe: no prompts or mutations to existing threads.
import { chromium, expect } from '@playwright/test'
import { spawn } from 'node:child_process'
import { randomBytes } from 'node:crypto'
import fs from 'node:fs/promises'
import path from 'node:path'
const origin = 'http://127.0.0.1:30013'
const root = path.resolve('..')
const output = path.resolve('test-results/codex-native')
await fs.mkdir(output, { recursive: true })
try { await fetch(origin + '/health'); throw new Error('Port occupied; not touching existing listener') } catch (error) { if (!String(error).includes('fetch failed')) throw error }
const work = await fs.mkdtemp(path.join(output, 'run-'))
const token = randomBytes(32).toString('hex')
const server = spawn(path.join(root, 'backend/.venv/Scripts/python.exe'), ['tests/mobile_fixture_server.py'], { cwd: path.join(root, 'backend'), windowsHide: true, stdio: 'ignore', env: { ...process.env, ASTRORDER_E2E_TOKEN: token, ASTRORDER_E2E_DATABASE: `sqlite:///${path.join(work, 'cache.db').replaceAll('\\', '/')}`, ASTRORDER_E2E_ATTACHMENTS: path.join(work, 'attachments') } })
let browser, context
const results = []
try {
  await expect.poll(async () => { try { return (await fetch(origin + '/health')).status } catch { return 0 } }, { timeout: 30000 }).toBe(200)
  browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  context = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  expect((await context.request.post(origin + '/api/v1/auth/session', { headers: { Origin: origin }, data: { token } })).ok()).toBe(true)
  const page = await context.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(origin + '/agents')
  const card = page.getByLabel('本机 Codex 连接')
  await card.getByRole('button', { name: '连接 Codex', exact: true }).click()
  await expect(card.getByText('已连接', { exact: true })).toBeVisible({ timeout: 60000 })
  const state = await (await context.request.get(origin + '/api/v1/connections/codex')).json()
  const bootstrap = await (await context.request.get(origin + '/api/v1/bootstrap')).json()
  const sessions = bootstrap.sessions.filter(s => s.agent_id === state.agent_id)
  expect(sessions.length).toBe(state.session_count)
  expect(new Set(sessions.map(s => s.id)).size).toBe(sessions.length)
  expect(bootstrap.agents.find(a => a.id === state.agent_id).kind).toBe('codex')
  await card.screenshot({ path: path.join(output, 'connected-desktop.png') })
  results.push({ check: 'native desktop connection and exact bootstrap readback', sessions: sessions.length, passed: true })
  if (sessions.length) {
    const response = await context.request.get(`${origin}/api/v1/sessions/${encodeURIComponent(sessions[0].id)}/messages?agent_id=${state.agent_id}&limit=2`)
    expect(response.ok()).toBe(true)
    const data = await response.json()
    expect(data.items.length).toBeLessThanOrEqual(2)
    results.push({ check: 'native two-item read without resume or prompts', items: data.items.length, has_more: Boolean(data.next_cursor), passed: true })
    if (data.next_cursor) {
      const older = await context.request.get(`${origin}/api/v1/sessions/${encodeURIComponent(sessions[0].id)}/messages?agent_id=${state.agent_id}&limit=20&before=${encodeURIComponent(data.next_cursor)}`)
      expect(older.ok()).toBe(true)
      expect((await older.json()).items.length).toBeLessThanOrEqual(20)
      results.push({ check: 'native older page through authenticated HTTP cursor', passed: true })
    }
    const binding = await context.request.get(`${origin}/api/v1/sessions/${encodeURIComponent(sessions[0].id)}/model?agent_id=${state.agent_id}`)
    if (!binding.ok()) throw new Error(`Native model metadata: ${binding.status()} ${await binding.text()}`)
    expect(typeof (await binding.json()).model).toBe('string')
    results.push({ check: 'native session model readback without transcript or prompts', passed: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto(origin + '/mobile/chat/fixture-main?agent_id=mobile-protocol-fixture')
  await page.getByRole('button', { name: 'Codex 连接', exact: true }).click()
  await expect(card.getByText('已连接', { exact: true })).toBeVisible()
  await card.screenshot({ path: path.join(output, 'connected-mobile.png') })
  await card.getByRole('button', { name: '断开 Codex', exact: true }).click()
  await expect(card.getByText('未连接', { exact: true })).toBeVisible()
  expect((await (await context.request.get(origin + '/api/v1/connections/codex')).json()).state).toBe('disconnected')
  results.push({ check: 'mobile connection controls and native disconnect readback', passed: true })
  expect(errors).toEqual([])
  await fs.writeFile(path.join(output, 'results.json'), JSON.stringify(results, null, 2))
  console.log(JSON.stringify({ scope: 'real Codex connection + reads; no prompts sent', checks: results.length, sessions: state.session_count, errors }))
} finally {
  if (context) await context.request.post(origin + '/api/v1/connections/codex/disconnect', { headers: { Origin: origin } }).catch(() => {})
  await browser?.close()
  if (server.exitCode === null) { server.kill(); await new Promise(resolve => server.once('exit', resolve)) }
}
