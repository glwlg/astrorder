import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'

// Isolated API and socket fixtures: no real credentials or Agent mutations.
const origin = process.env.ASTRORDER_ORDER_TEST_ORIGIN || 'http://127.0.0.1:30019'
const output = process.env.ASTRORDER_ORDER_TEST_OUTPUT || 'C:/Users/luwei/AppData/Local/hermes/cache/scratch/codex-order-browser.json'
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } })
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  page.on('console', message => { if (message.type() === 'error' && /same key/.test(message.text())) errors.push(message.text()) })
  const stamp = '1970-01-01T00:00:00Z' // Captured Codex native page's missing-timestamp representation.
  const sessions = ['one', 'two'].map(id => ({ id, agent_id: 'fixture-codex', title: `Order fixture ${id}`, status: 'running', workspace: null, updated_at: '2026-10-15T13:00:00Z' }))
  const row = (id, role, turn_id, session_id = 'one') => ({ id, role, turn_id, session_id, agent_id: 'fixture-codex', text: id, kind: role === 'tool' ? 'tool' : 'message', created_at: stamp, command_id: null, attachments: [], tool: role === 'tool' ? { name: 'commandExecution', status: 'running', arguments: 'fixture command' } : null })
  const old = [row('OLD_USER', 'user', 'old'), row('OLD_ANSWER', 'assistant', 'old')]
  const current = [row('CURRENT_USER', 'user', 'current'), row('CURRENT_PROGRESS', 'assistant', 'current'), row('CURRENT_TOOL', 'tool', 'current')]
  const other = [row('OTHER_USER', 'user', 'other', 'two'), row('OTHER_ANSWER', 'assistant', 'other', 'two')]
  let latest = old
  let socket
  let cursor = 0
  const emit = (type, data) => socket.send(JSON.stringify({ id: `fixture-event-${++cursor}`, cursor, type, agent_id: 'fixture-codex', session_id: 'one', data }))
  await page.routeWebSocket('**/ws/**', ws => { socket = ws })
  const reads = { one: 0, two: 0 }
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    const pathname = url.pathname
    let data = { items: [] }
    if (pathname.endsWith('/auth/session')) data = { authenticated: true }
    else if (pathname.endsWith('/bootstrap')) data = { protocol_version: 1, cursor: 0, agents: [{ id: 'fixture-codex', name: 'Codex fixture', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }], sessions, projects: [], commands: [] }
    else if (pathname.endsWith('/messages')) {
      const id = pathname.split('/').at(-2)
      reads[id]++
      data = { items: id === 'one' ? latest : other, next_cursor: null }
    } else if (pathname.endsWith('/sync')) data = sessions.find(s => pathname.includes(`/sessions/${s.id}/`))
    else if (pathname.endsWith('/model')) data = { model: 'fixture', provider: 'fixture', read_only: true }
    else if (pathname.endsWith('/models')) data = { items: [] }
    else if (pathname.endsWith('/preferences')) data = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
    else if (pathname.endsWith('/connections')) data = { local: { state: 'offline', available: false }, ssh: { items: [] } }
    await route.fulfill({ json: data })
  })
  await page.goto(`${origin}/chat/one?agent_id=fixture-codex`)
  const log = page.getByRole('log')
  await expect(log).toContainText('OLD_ANSWER')
  const navigate = async id => {
    // SPA navigation, deliberately never page.goto/reload between chats.
    await page.evaluate(target => {
      window.history.pushState({}, '', `/chat/${target}?agent_id=fixture-codex`)
      window.dispatchEvent(new PopStateEvent('popstate'))
    }, id)
    await expect(page.getByRole('heading', { level: 2, name: `Order fixture ${id}` })).toBeVisible()
  }
  await navigate('two')
  await expect(log).toContainText('OTHER_ANSWER')
  latest = current
  await expect.poll(() => Boolean(socket)).toBe(true)
  emit('transcript.invalidate', {})
  // Hook invalidation is debounced 250ms; wait for the inactive query to be marked stale.
  await page.waitForTimeout(400)
  await navigate('one')
  await expect(log).toContainText('CURRENT_PROGRESS')
  await expect(log).not.toContainText('OLD_ANSWER')
  await expect(log).not.toContainText('OLD_USER')
  emit('message.upsert', { ...row('OLD_ANSWER', 'assistant', 'old'), created_at: '2026-10-16T02:57:00Z' })
  await expect(log).not.toContainText('OLD_ANSWER')
  emit('message.upsert', row('CURRENT_LIVE_OUTPUT', 'assistant', 'current'))
  await expect(log).toContainText('CURRENT_LIVE_OUTPUT')
  for (let iteration = 0; iteration < 4; iteration++) {
    await navigate('two')
    await expect(log).toContainText('OTHER_ANSWER')
    await expect(log).not.toContainText('CURRENT_LIVE_OUTPUT')
    await navigate('one')
    await expect(log).toContainText('CURRENT_LIVE_OUTPUT')
    await expect(log).not.toContainText('OLD_ANSWER')
    await expect(page.locator('.transcript')).toHaveCount(1)
  }
  expect(errors).toEqual([])
  const result = { passed: true, scope: 'real SPA and websocket handling with isolated Codex API fixtures', round_trips: 5, history_reads: reads, old_reply_absent: true, new_live_output_preserved: true, page_errors: errors }
  await fs.writeFile(output, JSON.stringify(result, null, 2))
  console.log(JSON.stringify(result))
} finally {
  await browser.close()
}
