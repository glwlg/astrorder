import { chromium, expect } from '@playwright/test'
import fs from 'node:fs/promises'
const origin = process.env.ASTRORDER_ASYNC_TEST_ORIGIN || 'http://127.0.0.1:30039'
const output = process.env.ASTRORDER_ASYNC_TEST_OUTPUT || 'C:/Users/luwei/AppData/Local/hermes/cache/scratch/codex-async-input/browser-regression.json'
const browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
const results = []
try {
 for (const mobile of [false, true]) {
  const page = await browser.newPage({ viewport: mobile ? { width: 390, height: 844 } : { width: 1440, height: 1000 }, isMobile: mobile, hasTouch: mobile })
  const errors = []; page.on('pageerror', e => errors.push(e.message))
  const session = { id: 'async-fixture', agent_id: 'fixture-codex', title: 'Async fixture', status: 'idle', control_state: 'owned', workspace: null, updated_at: '2026-10-16T05:53:24Z' }
  const message = { id: 'call-question', agent_id: session.agent_id, session_id: session.id, role: 'assistant', kind: 'message', text: '', delivery: 'async', questions: [{ title: '喜欢哪种题材？', options: ['星际探索', '时间旅行', '人工智能'] }], attachments: [], tool: null, command_id: null, created_at: session.updated_at, turn_id: 'first' }
  let messages = [message], commands = [], posts = [], syncAnswers = 0, socket, cursor = 0
  await page.routeWebSocket('**/ws/**', ws => { socket = ws })
  await page.route('**/api/v1/**', async route => {
   const req = route.request(), path = new URL(req.url()).pathname; let data = { items: [] }
   if (path.endsWith('/auth/session')) data = { authenticated: true }
   else if (path.endsWith('/bootstrap')) data = { protocol_version: 1, cursor: 0, agents: [{ id: session.agent_id, name: 'Codex fixture', kind: 'codex', status: 'ready', capabilities: ['chat'], limitation: null }], sessions: [session], projects: [], commands }
   else if (path.endsWith('/preferences')) data = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
   else if (path.endsWith('/connections')) data = { local: { state: 'offline', available: false }, ssh: { items: [] } }
   else if (path.endsWith('/open-sessions')) data = { known_agent_ids: [session.agent_id], items: [{ agent_id: session.agent_id, id: session.id }] }
   else if (path.endsWith('/sync')) data = session
   else if (path.endsWith('/model')) data = { model: 'fixture', provider: 'fixture', read_only: false }
   else if (path.endsWith('/messages')) data = { items: messages, next_cursor: null }
   else if (path.endsWith('/commands') && req.method() === 'GET') data = { items: commands }
   else if (path.endsWith('/commands') && req.method() === 'POST') {
    const payload = req.postDataJSON(); posts.push(payload)
    data = { ...payload, state: 'accepted', error: null, attachments: [], created_at: session.updated_at }; commands = [data]
    messages = [...messages, { ...message, id: 'native-answer', role: 'user', text: payload.text, delivery: null, questions: null, turn_id: 'second' }]
   } else if (path.endsWith('/user-input')) syncAnswers++
   await route.fulfill({ json: data })
  })
  const open = () => page.goto(`${origin}/${mobile ? 'mobile/' : ''}chat/${session.id}?agent_id=${session.agent_id}`)
  await open()
  const card = page.getByRole('region', { name: '会话内异步询问' })
  await expect(card).toContainText('喜欢哪种题材？').catch(async error => { console.log({ mobile, url: page.url(), body: await page.locator('body').innerText(), errors }); throw error })
  await card.getByRole('radio', { name: '时间旅行' }).click()
  await card.getByRole('button', { name: '发送回答' }).click()
  await expect(card).toContainText(/回答已接收|已有后续输入/)
  expect(posts).toHaveLength(1); expect(posts[0]).toMatchObject({ action: 'send', text: '时间旅行', target_id: message.id, attachment_ids: [] }); expect(syncAnswers).toBe(0)
  await page.reload()
  await expect(card).toContainText(/回答已接收|已有后续输入/)
  await expect(card.getByRole('button', { name: '发送回答' })).toHaveCount(0)
  // A fresh native item arrives through the real socket consumer.
  const next = { ...message, id: 'live-question', turn_id: 'second', questions: [{ title: '还想看什么？', options: ['太空歌剧', '赛博朋克'] }] }
  messages = [...messages, next]
  await expect.poll(() => Boolean(socket)).toBe(true)
  socket.send(JSON.stringify({ id: `async-event-${++cursor}`, cursor, type: 'message.upsert', agent_id: session.agent_id, session_id: session.id, data: next }))
  await expect(page.getByRole('radio', { name: '赛博朋克' })).toBeVisible()
  await page.getByRole('radio', { name: '赛博朋克' }).click()
  expect(posts).toHaveLength(1); expect(errors).toEqual([])
  await page.screenshot({ path: output.replace('.json', mobile ? '-mobile.png' : '-desktop.png'), fullPage: true })
  results.push({ consumer: mobile ? 'mobile' : 'desktop', ordinary_send: true, refreshed_native_answer: true, live_question: true, sends: posts.length, synchronous_replies: syncAnswers, page_errors: errors })
  await page.close()
 }
 await fs.writeFile(output, JSON.stringify({ passed: true, scope: 'isolated API fixtures in real browser; no production Agent sends', results }, null, 2))
 console.log(JSON.stringify({ passed: true, results }))
} finally { await browser.close() }
