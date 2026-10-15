import { chromium, expect } from '@playwright/test'
import { createServer } from 'node:http'
import { randomBytes } from 'node:crypto'
import { readFile, mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

// Exercises the built SPA with inert native-shaped HTTP/WebSocket fixtures.
// No installed App Server, daemon, user conversations or credentials are used.
const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const dist = path.join(frontend, 'dist')
const output = process.env.ASTRORDER_MOBILE_RESULTS || path.join(frontend, 'test-results/mobile-feature-parity')
await mkdir(output, { recursive: true })
const mime = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.json': 'application/json', '.png': 'image/png', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.ico': 'image/x-icon' }
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, 'http://127.0.0.1')
    if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/ws/')) {
      response.writeHead(503).end('Fixture route missing')
      return
    }
    if (url.pathname === '/__fixture_storage__') {
      response.writeHead(200, { 'Content-Type': 'text/html', 'Cache-Control': 'no-store' }).end('<!doctype html><title>Inert fixture storage</title>')
      return
    }
    const requested = path.resolve(dist, '.' + decodeURIComponent(url.pathname))
    if (requested !== dist && !requested.startsWith(dist + path.sep)) {
      response.writeHead(403).end()
      return
    }
    let file = requested
    let content
    try { content = await readFile(file) } catch { file = path.join(dist, 'index.html'); content = await readFile(file) }
    response.writeHead(200, { 'Content-Type': mime[path.extname(file)] || 'application/octet-stream', 'Cache-Control': 'no-store' }).end(content)
  } catch { response.writeHead(500).end() }
})
await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve) })
const origin = `http://127.0.0.1:${server.address().port}`
const results = []
let browser
const agentId = 'mobile-parity-inert'
const timestamp = '2026-10-14T00:00:00Z'
const workspace = 'C:/mobile-parity-fixture'
const filePath = workspace + '/notes.md'
const fixturePng = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/ZfoAAAAASUVORK5CYII=', 'base64')
const summary = () => ({ scope: 'built frontend + isolated HTTP/WebSocket fixtures; no native Agent or phone device', passed: results.filter(result => result.status === 'passed').length, failed: results.filter(result => result.status === 'failed').length, results })

function task(id, title, kind, status, progress = {}, targetId = null) {
  return { id, title, kind, status, progress, target_id: targetId, agent_id: agentId, session_id: 'parent', command: null, logs: [], created_at: timestamp, updated_at: timestamp }
}

async function fixture(viewport = { width: 390, height: 844 }, options = {}) {
  const credential = randomBytes(24).toString('hex')
  const context = await browser.newContext({ viewport, isMobile: !options.desktop, hasTouch: !options.desktop, reducedMotion: 'reduce', serviceWorkers: 'block', colorScheme: options.theme || 'dark' })
  await context.addInitScript(({ credential, fixtureOrigin }) => {
    if (location.origin === fixtureOrigin) localStorage.setItem('astrorder:token', credential)
  }, { credential, fixtureOrigin: origin })
  const state = {
    locked: Boolean(options.locked), branch: 'feature/mobile', model: 'fixture-model', effort: 'medium', mode: 'manual',
    fileContent: '# Mobile fixture\n\nOriginal document.\n', commands: [], requests: [], terminal: [], errors: [], unknown: [], goal: null,
    events: [], messages: [], uploads: [], eventOffline: false, httpOffline: false, ambiguousNext: false, hideAmbiguousReceipt: false,
    sessions: ['parent', 'child', 'other'].map(id => ({ id, agent_id: agentId, title: id === 'parent' ? '移动端完整功能验收' : id === 'child' ? '子代理会话' : '另一个会话', workspace, status: id === 'child' || (id === 'parent' && options.busy) ? 'running' : 'idle', updated_at: timestamp, control_state: options.locked && id === 'parent' ? 'readonly' : 'owned', is_open: true, ...(id === 'child' ? { parent_session_id: 'parent', parent_agent_id: agentId } : {}) })),
    tasks: [
      task('child-task', '检查移动端交互', 'subagent', 'running', { child_session_id: 'child', native_status: 'running', model: 'fixture-child-model', input_tokens: 123, output_tokens: 45, blocking: false, summary: '检查子代理结果' }, 'child'),
      task('todo-task', '修复输入锁', 'todo', 'completed', { completed: 1, total: 2 }),
      task('plan-task', '验收手机工作区', 'todo', 'pending', { type: 'plan' }),
      task('tool-task', '读取文件记录', 'tool', 'completed', { tool_count: 1, summary: '文件读取完成' }),
    ],
  }
  const agent = { id: agentId, kind: 'hermes', name: '隔离验收 Hermes', status: 'ready', capabilities: ['chat', 'stop', 'queue', 'attachments', 'approvals', 'events', 'history'], limitation: null }
  const preferences = { appearance: {}, session_pins: {}, pinned_projects: [], project_order: [] }
  const goalTask = () => state.goal ? [task('hermes:goal:current', '目标: ' + state.goal.title, 'todo', state.goal.status === 'paused' ? 'pending' : 'running', { ...state.goal, type: 'goal', source: 'hermes', native_status: state.goal.status, blocking: false })] : []
  const message = { id: 'file-message', agent_id: agentId, session_id: 'parent', role: 'assistant', kind: 'message', text: '工作区文件：[notes.md](notes.md)', attachments: [], created_at: timestamp, command_id: null, tool: null }
  await context.route(origin + '/api/**', async route => {
    const request = route.request()
    const url = new URL(request.url())
    const pathname = url.pathname.replace('/api/v1', '')
    const method = request.method()
    if (state.httpOffline) return route.abort('internetdisconnected')
    const multipart = (request.headers()['content-type'] || '').startsWith('multipart/form-data')
    const body = multipart ? null : request.postDataJSON()
    state.requests.push({ pathname, method, body, query: Object.fromEntries(url.searchParams), authenticated: request.headers().authorization === `Bearer ${credential}` })
    const json = (value, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) })
    const cookieAuthenticated = request.headers().cookie?.split(';').some(cookie => cookie.trim() === `astrorder_session=${credential}`)
    if (request.headers().authorization !== `Bearer ${credential}` && url.searchParams.get('token') !== credential && !cookieAuthenticated) return json({ detail: 'Fixture authentication required' }, 401)
    if (pathname === '/auth/session') return route.fulfill({ status: 200, contentType: 'application/json', headers: { 'Set-Cookie': `astrorder_session=${credential}; Path=/; HttpOnly; SameSite=Lax` }, body: JSON.stringify({ authenticated: true }) })
    if (pathname === '/bootstrap') return json({ protocol_version: 1, cursor: 0, agents: [agent], sessions: state.sessions, tasks: [...state.tasks, ...goalTask()], projects: [] })
    if (pathname === '/open-sessions') return json({ known_agent_ids: [agentId], items: state.sessions.map(({ id, agent_id }) => ({ id, agent_id })), live: [] })
    if (pathname === '/presence') return json({ items: [], open: state.sessions.map(({ id, agent_id }) => ({ id, agent_id })), live: [] })
    if (pathname.startsWith('/preferences')) return json(preferences)
    if (pathname === '/connections') return json({ local: { kind: 'hermes', state: 'connected', available: true, version: null, agent_id: agentId, profile_name: 'fixture', session_id: 'parent', detail: '' }, ssh: { items: [], state: 'unconfigured', settings: null, detail: '' } })
    if (pathname === '/analytics/model-quota') return json({ model: state.model, providers: [], reports: [], accounts: [] })
    if (pathname === '/analytics/usage') return json({ range: '7d', surface: 'all', since: 0, generatedAt: timestamp, summary: {}, days: [], models: [], providers: [], accounts: [], historyTruncated: false, truncatedPrefixBytes: 0, entriesTruncated: false, entriesDropped: 0 })
    if (pathname === '/background-tasks' || pathname === '/bot-groups' || pathname === '/review-relay/runs') return json({ items: [] })
    if (pathname === '/attachments' && method === 'POST') {
      const upload = request.postDataBuffer()
      const filename = upload.toString('latin1').match(/filename="([^"]+)"/)?.[1] || 'fixture.png'
      const attachment = { id: `fixture-upload-${state.uploads.length + 1}`, name: filename, media_type: 'image/png', url: '/api/v1/attachments/fixture-image' }
      state.uploads.push({ attachment, bytes: upload.length })
      return json(attachment)
    }
    if (pathname.startsWith('/attachments/') && method === 'GET') return route.fulfill({ contentType: 'image/png', body: fixturePng })
    if (pathname === '/commands' && method === 'POST') {
      const command = { ...body, state: body.text === 'REJECT' ? 'failed' : body.text === 'HOLD' ? 'accepted' : 'completed', error: body.text === 'REJECT' ? 'Fixture rejection' : null, attachments: state.uploads.filter(item => body.attachment_ids.includes(item.attachment.id)).map(item => item.attachment), created_at: new Date().toISOString() }
      if (body.text === 'RETRY_ONCE' && !state.commands.some(item => item.text === 'RETRY_ONCE')) { command.state = 'failed'; command.error = '原生确认未发送' }
      if (body.text === 'HOLD') state.sessions.find(item => item.id === body.session_id).status = 'running'
      state.commands.push(command)
      if (body.action === 'send' && command.state !== 'failed') state.messages.push({ id: `native-${body.id}`, agent_id: body.agent_id, session_id: body.session_id, role: 'user', kind: 'message', text: body.text, attachments: command.attachments, command_id: body.id, created_at: command.created_at, tool: null })
      if (body.action === 'send' && body.text.startsWith('/goal ')) state.goal = { title: body.text.slice(6), status: 'active', turns_used: 0, max_turns: 20 }
      if (body.action === 'stop' && body.session_id === 'child') {
        state.tasks[0] = { ...state.tasks[0], status: 'completed' }
        state.sessions[1].status = 'idle'
      }
      if (state.ambiguousNext && body.action === 'send') {
        state.ambiguousNext = false
        return route.abort('connectionreset')
      }
      return json(command)
    }
    const sessionMatch = pathname.match(/^\/sessions\/([^/]+)\/(.+)$/)
    if (sessionMatch) {
      const [, id, operation] = sessionMatch
      const session = state.sessions.find(item => item.id === id)
      if (operation === 'sync') {
        if (options.unlockOnRetry && state.requests.filter(item => item.pathname === pathname).length > 1) {
          state.locked = false
          session.control_state = 'owned'
        }
        return json(session)
      }
      if (operation === 'model') {
        if (method === 'POST') state.model = body.model
        return json({ provider: 'fixture', model: state.model, effort: state.effort, branch: state.branch, read_only: id === 'parent' && state.locked })
      }
      if (operation === 'models') return json({ items: [{ provider: 'fixture', model: 'fixture-model', label: 'Fixture · current' }, { provider: 'fixture', model: 'fixture-next', label: 'Fixture · next' }] })
      if (operation === 'reasoning') { state.effort = body.effort; return json({ effort: state.effort }) }
      if (operation === 'approval-mode') { if (method === 'POST') state.mode = body.mode; return json({ mode: state.mode }) }
      if (operation === 'messages') return json({ items: [...(id === 'parent' ? [message] : []), ...state.messages.filter(item => item.session_id === id)], next_cursor: null })
      if (operation === 'commands') return json({ items: state.commands.filter(item => item.session_id === id && !(state.hideAmbiguousReceipt && item.text === '未知结果只核对')) })
      if (operation === 'background-processes') return json({ items: [] })
      if (operation === 'tasks') return json({ items: id === 'parent' ? [...state.tasks, ...goalTask()] : [] })
      if (operation === 'agent-commands') return json({ items: [{ name: 'goal', description: '设置原生目标', input_hint: '目标内容' }] })
      if (operation === 'agent-mentions') return json({ items: [] })
      if (operation === 'usage') return json({ ok: true, session_id: id, model: state.model, context_window: 200000, last_input_tokens: 24000, context_available: true, source: 'native_last_request', used_percentage: 12, total_tokens: 50000, input_tokens: 40000, output_tokens: 10000, cached_tokens: 20000, reasoning_tokens: 0, cache_hit_rate: 50 })
      if (operation === 'goal') {
        if (method === 'DELETE') state.goal = null
        if (method === 'PUT') {
          const { objective, ...rest } = body
          state.goal = { ...(state.goal || { status: 'active', turns_used: 0, max_turns: 20 }), ...rest, ...(objective === undefined ? {} : { title: objective }) }
        }
        return json({ source: 'hermes', goal: state.goal })
      }
    }
    if (pathname === '/git/status') return json({ branch: state.branch, branches: ['feature/mobile', 'feature/next'], insertions: 3, deletions: 1, changed_files: 1 })
    if (pathname === '/git/branch') { state.branch = body.branch; return json({ status: 'ok', branch: state.branch }) }
    if (pathname === '/git/diff') return json({ files: [], insertions: 0, deletions: 0 })
    if (pathname === '/files/search') return json({ items: [{ name: 'notes.md', path: filePath }] })
    if (pathname === '/files/tree') return json({ root: workspace, items: [{ name: 'notes.md', path: filePath, is_dir: false }] })
    if (pathname === '/files/raw') return route.fulfill({ status: 200, contentType: 'text/plain; charset=utf-8', body: state.fileContent })
    if (pathname === '/system/write-file') { state.fileContent = body.content; return json({ status: 'ok', path: body.path }) }
    state.unknown.push({ pathname, method })
    return json({ detail: 'Unknown fixture operation: ' + pathname }, 404)
  })
  await context.routeWebSocket('**/ws/v1/**', ws => {
    const url = new URL(ws.url())
    expect(url.searchParams.get('token')).toBe(credential)
    if (url.pathname.endsWith('/terminal')) {
      const entry = { agentId: url.searchParams.get('agent_id'), sessionId: url.searchParams.get('session_id'), frames: [] }
      state.terminal.push(entry)
      ws.onMessage(message => entry.frames.push(String(message)))
      ws.send(Buffer.from('Mobile terminal binary output\r\n', 'utf8'))
    } else if (url.pathname.endsWith('/events')) {
      state.events.push({ socket: ws, after: Number(url.searchParams.get('after')), closed: state.eventOffline })
      if (state.eventOffline) ws.close({ code: 1001, reason: 'Fixture offline' })
    }
  })
  const page = await context.newPage()
  page.on('pageerror', error => state.errors.push(error.message))
  const open = async (id = 'parent') => {
    await page.goto(`${origin}/${options.desktop ? '' : 'mobile/'}chat/${id}?agent_id=${agentId}`)
    if (options.desktop) await expect(page.getByLabel('消息内容')).toBeVisible()
    else await expect(page.locator('.m-card-head h1')).toHaveText(state.sessions.find(session => session.id === id).title)
    if (options.desktop) await expect.poll(() => state.events.length).toBeGreaterThan(0)
    else await expect(page.getByLabel('已连接', { exact: true })).toBeVisible()
  }
  const menu = async name => { await page.getByRole('button', { name: '会话操作', exact: true }).click(); await page.getByRole('menuitem', { name, exact: true }).click() }
  const screenshot = name => page.screenshot({ path: path.join(output, name + '.png') })
  const close = async () => {
    await context.close()
    expect(state.errors).toEqual([])
    expect(state.unknown).toEqual([])
  }
  if (options.seedQueue?.length) {
    await page.goto(origin + '/__fixture_storage__')
    await page.evaluate(seedQueue => new Promise((resolve, reject) => {
      const request = indexedDB.open('astrorder:mobile', 1)
      request.onupgradeneeded = () => request.result.createObjectStore('outbox')
      request.onerror = () => reject(request.error)
      request.onsuccess = () => {
        const db = request.result
        const transaction = db.transaction('outbox', 'readwrite')
        transaction.objectStore('outbox').put(seedQueue, 'entries')
        transaction.oncomplete = () => { db.close(); resolve() }
        transaction.onerror = () => { db.close(); reject(transaction.error) }
      }
    }), options.seedQueue)
  }
  const readQueue = () => page.evaluate(() => new Promise((resolve, reject) => {
    const request = indexedDB.open('astrorder:mobile', 1)
    request.onerror = () => reject(request.error)
    request.onsuccess = () => {
      const db = request.result
      const transaction = db.transaction('outbox', 'readonly')
      const entries = transaction.objectStore('outbox').get('entries')
      entries.onsuccess = () => resolve(entries.result || [])
      entries.onerror = () => reject(entries.error)
      transaction.oncomplete = () => db.close()
    }
  }))
  const disconnect = async () => {
    state.eventOffline = true
    state.httpOffline = true
    for (const event of state.events) if (!event.closed) { event.closed = true; event.socket.close({ code: 1001, reason: 'Fixture disconnect' }) }
    await expect(page.getByLabel('已连接', { exact: true })).toHaveCount(0)
  }
  const reconnect = async () => {
    state.eventOffline = false
    state.httpOffline = false
    await page.evaluate(() => window.dispatchEvent(new Event('online')))
    await expect(page.getByLabel('已连接', { exact: true })).toBeVisible()
  }
  await open()
  return { page, context, state, open, menu, screenshot, close, readQueue, disconnect, reconnect }
}

async function check(name, run) {
  if (process.env.ASTRORDER_MOBILE_FILTER && !new RegExp(process.env.ASTRORDER_MOBILE_FILTER).test(name)) return
  try { await run(); results.push({ name, status: 'passed' }); console.log('PASS ' + name) }
  catch (error) { results.push({ name, status: 'failed', error: String(error.stack || error) }); console.log('FAIL ' + name + ': ' + error.message) }
  finally { await writeFile(path.join(output, 'results.json'), JSON.stringify(summary(), null, 2)) }
}

try {
  browser = await chromium.launch({ executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true })
  await check('locked composer blocks controls and retries the exact native session', async () => {
    const f = await fixture(undefined, { locked: true, unlockOnRetry: true })
    try {
      await expect(f.page.getByText('已在另一个应用中打开')).toBeVisible()
      await expect(f.page.getByLabel('消息内容')).toHaveCount(0)
      await f.page.getByRole('button', { name: '会话操作', exact: true }).click()
      await expect(f.page.getByRole('menuitem', { name: '继续', exact: true })).toHaveAttribute('data-disabled', 'true')
      await f.page.keyboard.press('Escape')
      expect(f.state.commands).toHaveLength(0)
      await f.screenshot('locked-composer')
      await f.page.getByRole('button', { name: '重试', exact: true }).click()
      await expect(f.page.getByLabel('消息内容')).toBeVisible()
      expect(f.state.locked).toBe(false)
      expect(f.state.requests.filter(item => item.pathname.endsWith('/sync')).every(item => item.query.agent_id === agentId)).toBe(true)
    } finally { await f.close() }
  })
  for (const desktop of [false, true]) {
    await check(`empty goals stay silent and Agent command creation remains available on ${desktop ? 'desktop' : 'mobile'}`, async () => {
      const f = await fixture(desktop ? { width: 1440, height: 960 } : undefined, { desktop })
      try {
        await expect.poll(() => f.state.events.length).toBeGreaterThan(0)
        await expect(f.page.getByRole('button', { name: '设置目标', exact: true })).toHaveCount(0)
        await expect(f.page.locator('.composer-goal-pill, .m-goal-summary')).toHaveCount(0)
        const tombstone = task('hermes:goal:current', '目标', 'todo', 'cancelled', { type: 'goal', source: 'hermes', blocking: false })
        let cursor = 0
        const emit = data => {
          const event = { id: `goal-entry-${++cursor}`, cursor, type: 'task.upsert', agent_id: agentId, session_id: 'parent', data }
          f.state.events.at(-1).socket.send(JSON.stringify(event))
        }
        emit(tombstone)
        const failure = task('failure-control', '隔离失败对照', 'background', 'failed')
        emit(failure)
        await expect(f.page.getByText('隔离失败对照未完成。（点击跳转）', { exact: true })).toBeVisible()
        await expect(f.page.getByText('目标已取消。（点击跳转）', { exact: true })).toHaveCount(0)
        await expect(f.page.getByText('任务失败', { exact: true })).toHaveCount(1)
        await expect(f.page.locator('.composer-goal-pill, .m-goal-summary')).toHaveCount(0)
        await f.screenshot(`empty-goal-${desktop ? 'desktop' : 'mobile'}`)
        await f.page.getByLabel('消息内容').fill('/go')
        await f.page.getByRole('option', { name: /\/goal/ }).click()
        await expect(f.page.getByLabel('消息内容')).toHaveValue('/goal ')
        await f.page.getByLabel('消息内容').fill('/goal 命令创建的目标')
        await f.page.getByRole('button', { name: '发送', exact: true }).click()
        await expect.poll(() => f.state.goal?.title).toBe('命令创建的目标')
        const active = { ...tombstone, title: '目标: 命令创建的目标', status: 'running', progress: { type: 'goal', source: 'hermes', native_status: 'active', title: '命令创建的目标', turns_used: 0, max_turns: 20, blocking: false } }
        emit(active)
        await expect(f.page.getByText('命令创建的目标', { exact: true }).or(f.page.getByRole('button', { name: '目标管理', exact: true }))).toBeVisible()
        emit(tombstone)
        await expect(f.page.locator('.composer-goal-pill, .m-goal-summary')).toHaveCount(0)
        await expect(f.page.getByRole('button', { name: '设置目标', exact: true })).toHaveCount(0)
        await expect(f.page.getByText('目标已取消。（点击跳转）', { exact: true })).toHaveCount(0)
        const newFailure = task('failure-after-clear', '清除后的失败对照', 'background', 'failed')
        emit(newFailure)
        await expect(f.page.getByText('清除后的失败对照未完成。（点击跳转）', { exact: true })).toBeVisible()
        expect(f.state.requests.filter(item => item.pathname.endsWith('/goal'))).toHaveLength(0)
      } finally { await f.close() }
    })
  }
  for (const desktop of [false, true]) {
    await check(`owned active guidance stays sendable and uncertain delivery is not repeated on ${desktop ? 'desktop' : 'mobile'}`, async () => {
      const f = await fixture(desktop ? { width: 1440, height: 960 } : undefined, { desktop })
      try {
        const input = f.page.getByLabel('消息内容')
        await input.fill('HOLD')
        await f.page.getByRole('button', { name: '发送', exact: true }).click()
        await expect.poll(() => f.state.commands.length).toBe(1)
        const active = f.state.commands[0]
        expect(active.state).toBe('accepted')
        f.state.events.at(-1).socket.send(JSON.stringify({ id: 'running-owned', cursor: 1, type: 'session.upsert', agent_id: agentId, session_id: 'parent', data: f.state.sessions[0] }))
        await input.fill('原任务继续时补充引导')
        await f.page.getByRole('button', { name: desktop ? '加入队列' : '发送', exact: true }).click()
        const row = f.page.getByLabel('排队消息', { exact: true }).filter({ hasText: '原任务继续时补充引导' })
        await expect(row).toBeVisible()
        expect(f.state.commands).toHaveLength(1)
        await row.getByRole('button', { name: '立即引导', exact: true }).click()
        await expect.poll(() => f.state.commands.length).toBe(2)
        await expect(row).toHaveCount(0)
        expect(f.state.commands[0]).toMatchObject({ id: active.id, state: 'accepted' })
        expect(f.state.sessions[0].status).toBe('running')
        await input.fill('RETRY_ONCE')
        await f.page.getByRole('button', { name: desktop ? '加入队列' : '发送', exact: true }).click()
        const retry = f.page.getByLabel('排队消息', { exact: true }).filter({ hasText: 'RETRY_ONCE' })
        await retry.getByRole('button', { name: '立即引导', exact: true }).click()
        await expect.poll(() => f.state.commands.length).toBe(3)
        const failedId = f.state.commands[2].id
        if (!desktop) {
          const toast = f.page.locator('.mantine-Notification-root').filter({ hasText: '原生确认未发送' })
          if (await toast.count()) await toast.getByRole('button').click()
        }
        await retry.getByRole('button', { name: desktop ? '重试' : '重新发送', exact: true }).click()
        await expect.poll(() => f.state.commands.length).toBe(4)
        expect(f.state.commands[3].id).not.toBe(failedId)
        await expect(retry).toHaveCount(0)
        await input.fill('未知结果只核对')
        await f.page.getByRole('button', { name: desktop ? '加入队列' : '发送', exact: true }).click()
        const unknown = f.page.getByLabel('排队消息', { exact: true }).filter({ hasText: '未知结果只核对' })
        f.state.ambiguousNext = true
        f.state.hideAmbiguousReceipt = true
        await unknown.getByRole('button', { name: '立即引导', exact: true }).click()
        await expect.poll(() => f.state.commands.length).toBe(5)
        await expect(unknown.getByRole('button', { name: desktop ? '核对结果' : '核对发送结果', exact: true })).toBeVisible()
        f.state.hideAmbiguousReceipt = false
        await unknown.getByRole('button', { name: desktop ? '核对结果' : '核对发送结果', exact: true }).click()
        await expect(unknown).toHaveCount(0)
        expect(f.state.commands).toHaveLength(5)
        expect(new Set(f.state.commands.map(command => command.id)).size).toBe(5)
        await f.screenshot(`owned-guidance-${desktop ? 'desktop' : 'mobile'}`)
      } finally { await f.close() }
    })
  }
  await check('goal command creation pause resume edit delete uses confirmed session-scoped requests', async () => {
    const f = await fixture()
    try {
      await expect(f.page.getByRole('button', { name: '目标管理', exact: true })).toHaveCount(0)
      await expect(f.page.getByRole('button', { name: '设置目标', exact: true })).toHaveCount(0)
      await f.page.getByLabel('消息内容').fill('/go')
      await f.page.getByRole('option', { name: /\/goal/ }).click()
      await expect(f.page.getByLabel('消息内容')).toHaveValue('/goal ')
      await f.page.getByLabel('消息内容').fill('/goal 完成手机端功能对齐并验证全部操作')
      await f.page.getByRole('button', { name: '发送', exact: true }).click()
      await expect.poll(() => f.state.commands.find(command => command.text.startsWith('/goal '))?.text).toBe('/goal 完成手机端功能对齐并验证全部操作')
      await f.open()
      await f.page.getByRole('button', { name: '目标管理', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '暂停目标', exact: true })).toBeVisible()
      expect(f.state.goal.title).toBe('完成手机端功能对齐并验证全部操作')
      await f.page.getByRole('button', { name: '暂停目标', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '恢复目标', exact: true })).toBeVisible()
      expect(f.state.goal.status).toBe('paused')
      await f.screenshot('goal-paused')
      await f.page.getByRole('button', { name: '恢复目标', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '暂停目标', exact: true })).toBeVisible()
      await f.page.getByRole('button', { name: '修改目标', exact: true }).click()
      await f.page.getByLabel('目标内容').fill('修订后的手机端目标')
      await f.page.getByRole('button', { name: '保存目标', exact: true }).click()
      await expect(f.page.getByRole('dialog', { name: '修改目标', exact: true })).toHaveCount(0)
      expect(f.state.goal.title).toBe('修订后的手机端目标')
      await f.page.getByRole('button', { name: '删除目标', exact: true }).click()
      const confirm = f.page.getByRole('dialog', { name: '删除目标？', exact: true })
      expect(f.state.goal).not.toBeNull()
      await confirm.getByRole('button', { name: '删除目标', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '修改目标', exact: true })).toHaveCount(0)
      await expect(f.page.getByRole('button', { name: '设置目标', exact: true })).toHaveCount(0)
      await expect(f.page.getByRole('button', { name: '目标管理', exact: true })).toHaveCount(0)
      expect(f.state.goal).toBeNull()
      const mutations = f.state.requests.filter(item => item.pathname.endsWith('/goal'))
      expect(mutations).toHaveLength(4)
      expect(mutations.every(item => item.query.agent_id === agentId && item.pathname === '/sessions/parent/goal' && item.authenticated)).toBe(true)
    } finally { await f.close() }
  })
  await check('todos plans and child details navigation and stop preserve child identity', async () => {
    const f = await fixture()
    try {
      await f.page.getByRole('button', { name: '任务与子代理', exact: true }).click()
      await f.page.getByRole('button', { name: '展开运行详情', exact: true }).click()
      const list = f.page.getByLabel('运行任务列表')
      for (const title of ['修复输入锁', '验收手机工作区', '检查移动端交互']) await expect(list.getByRole('button', { name: title, exact: true })).toBeVisible()
      await expect(list.getByRole('button', { name: '读取文件记录', exact: true })).toHaveCount(0)
      await f.screenshot('tasks-and-subagents')
      await list.getByRole('button', { name: '检查移动端交互', exact: true }).click()
      await expect(f.page.getByText('模型：fixture-child-model', { exact: true })).toBeVisible()
      await expect(f.page.getByText('检查子代理结果', { exact: true })).toBeVisible()
      await f.page.getByRole('button', { name: '停止任务', exact: true }).click()
      expect(f.state.commands).toHaveLength(0)
      await f.page.getByRole('dialog', { name: '停止任务？', exact: true }).getByRole('button', { name: '停止任务', exact: true }).click()
      await expect.poll(() => f.state.commands.length).toBe(1)
      expect(f.state.commands[0]).toMatchObject({ action: 'stop', agent_id: agentId, session_id: 'child', target_id: 'child' })
      await f.page.getByRole('button', { name: '打开子会话', exact: true }).click()
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['子代理会话'])
      expect(new URL(f.page.url()).searchParams.get('agent_id')).toBe(agentId)
    } finally { await f.close() }
  })
  await check('native usage model reasoning and approval controls stay operable on mobile', async () => {
    const f = await fixture()
    try {
      await expect(f.page.getByRole('button', { name: '上下文用量', exact: true })).toHaveText('24.0k / 200k')
      await f.page.getByRole('button', { name: '上下文用量', exact: true }).click()
      await expect(f.page.getByLabel('运行信息')).toContainText('缓存命中率 50%')
      await f.screenshot('runtime-usage')
      await f.page.getByRole('button', { name: '关闭面板', exact: true }).click()
      await f.page.getByRole('button', { name: '选择会话模型', exact: true }).click()
      await f.page.getByRole('button', { name: /^帮我批准/ }).click()
      await expect.poll(() => f.state.mode).toBe('auto')
      await expect(f.page.getByRole('button', { name: /^帮我批准/ })).toHaveAttribute('aria-pressed', 'true')
      await f.page.getByRole('button', { name: '思考强度 高', exact: true }).click()
      await expect.poll(() => f.state.effort).toBe('high')
      await f.page.getByRole('button', { name: 'Fixture · next', exact: true }).click()
      await f.page.getByRole('button', { name: '确认切换模型', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '选择会话模型', exact: true })).toContainText('fixture-next')
      expect(f.state.model).toBe('fixture-next')
    } finally { await f.close() }
  })
  await check('chat file opens editable workspace and preserves unsaved content across close and session switch', async () => {
    const f = await fixture()
    try {
      await f.page.getByRole('link', { name: 'notes.md', exact: true }).click()
      await expect(f.page.getByRole('dialog', { name: '会话工作区', exact: true })).toBeVisible()
      await expect(f.page.getByText('Original document.', { exact: true })).toBeVisible()
      await f.page.getByRole('button', { name: '编辑 Markdown', exact: true }).click()
      const editor = f.page.locator('.monaco-editor .view-lines')
      await expect(editor).toBeVisible({ timeout: 30000 })
      await editor.click()
      await f.page.keyboard.press('Control+a')
      await f.page.keyboard.insertText('# Unsaved mobile draft\n\nPreserve this edit.')
      await expect(f.page.getByText('● 未保存', { exact: true })).toBeVisible()
      await f.page.getByRole('button', { name: '关闭工作区', exact: true }).click()
      await f.page.keyboard.press('Control+s')
      expect(f.state.requests.filter(item => item.pathname === '/system/write-file')).toHaveLength(0)
      await f.menu('工作区')
      await expect(f.page.getByText('● 未保存', { exact: true })).toBeVisible()
      await expect(f.page.locator('.monaco-editor .view-lines')).toContainText('Unsaved mobile draft')
      await f.screenshot('workspace-unsaved-file')
      await f.page.getByRole('button', { name: '关闭工作区', exact: true }).click()
      await f.page.getByRole('button', { name: '打开会话列表', exact: true }).click()
      await f.page.getByRole('dialog', { name: '会话列表', exact: true }).getByRole('button', { name: '另一个会话', exact: true }).click()
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['另一个会话'])
      await f.menu('工作区')
      await expect(f.page.getByText('● 未保存', { exact: true })).toBeHidden()
      await f.page.getByRole('button', { name: '关闭工作区', exact: true }).click()
      await f.page.getByRole('button', { name: '打开会话列表', exact: true }).click()
      await f.page.getByRole('dialog', { name: '会话列表', exact: true }).getByRole('button', { name: '移动端完整功能验收', exact: true }).click()
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['移动端完整功能验收'])
      await f.menu('工作区')
      await expect(f.page.getByText('● 未保存', { exact: true })).toBeVisible()
      await expect(f.page.locator('.monaco-viewer-pane:visible .view-lines')).toContainText('Unsaved mobile draft')
      await f.page.getByRole('button', { name: '保存', exact: true }).click()
      await expect.poll(() => f.state.fileContent).toContain('Preserve this edit.')
      const saved = f.state.requests.find(item => item.pathname === '/system/write-file')
      expect(saved).toMatchObject({ authenticated: true, body: { path: filePath, session_id: 'parent' } })
      await expect(f.page.getByText('● 未保存', { exact: true })).toHaveCount(0)
    } finally { await f.close() }
  })
  await check('mobile terminal scopes identity, renders binary output and resizes only when visible', async () => {
    const f = await fixture()
    try {
      await f.menu('工作区')
      await f.page.getByRole('button', { name: /^终端/ }).click()
      await expect(f.page.locator('.xterm-screen')).toBeVisible()
      await expect.poll(() => f.state.terminal.length).toBe(1)
      expect(f.state.terminal[0]).toMatchObject({ agentId, sessionId: 'parent' })
      await expect.poll(() => f.state.terminal[0].frames.filter(frame => frame.includes('resize')).length).toBeGreaterThan(0)
      expect(await f.page.locator('.xterm-helper-textarea').evaluate(element => document.activeElement === element)).toBe(false)
      await f.screenshot('mobile-terminal')
      await f.page.getByRole('button', { name: '关闭工作区', exact: true }).click()
      await f.page.setViewportSize({ width: 360, height: 740 })
      const hiddenFrames = f.state.terminal[0].frames.length
      await f.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      expect(f.state.terminal[0].frames).toHaveLength(hiddenFrames)
      await f.menu('工作区')
      await expect.poll(() => f.state.terminal[0].frames.length).toBeGreaterThan(hiddenFrames)
      const last = JSON.parse(f.state.terminal[0].frames.at(-1))
      expect(last.cols).toBeGreaterThan(0)
      expect(last.rows).toBeGreaterThan(0)
    } finally { await f.close() }
  })
  await check('queued messages support explicit guidance edit retry delete and keep uploaded attachments', async () => {
    const uploaded = { id: 'already-uploaded', name: 'note.txt', media_type: 'text/plain', url: '/api/v1/attachments/already-uploaded' }
    const entry = (id, text, state = 'queued', attachments = []) => ({ payload: { id, agent_id: agentId, session_id: 'parent', action: 'send', text, attachment_ids: attachments.map(item => item.id), target_id: null }, files: [], attachments, state })
    const f = await fixture(undefined, { busy: true, seedQueue: [entry('guide', '立即修正方向'), entry('edit', '带附件的待发内容', 'queued', [uploaded]), entry('retry', '重试失败消息', 'failed'), entry('delete', '删除这个待发消息')] })
    try {
      const queue = f.page.getByLabel('排队消息', { exact: true })
      await expect(queue).toHaveCount(4)
      expect(f.state.commands).toHaveLength(0)
      await queue.filter({ hasText: '立即修正方向' }).getByRole('button', { name: '立即引导', exact: true }).click()
      await expect.poll(() => f.state.commands.length).toBe(1)
      expect(f.state.commands[0]).toMatchObject({ id: 'guide', text: '立即修正方向', agent_id: agentId, session_id: 'parent', action: 'send' })
      await expect(queue.filter({ hasText: '立即修正方向' })).toHaveCount(0)
      await queue.filter({ hasText: '重试失败消息' }).getByRole('button', { name: '重新发送', exact: true }).click()
      await expect.poll(() => f.state.commands.length).toBe(2)
      expect(f.state.commands[1]).toMatchObject({ text: '重试失败消息', agent_id: agentId, session_id: 'parent', action: 'send' })
      expect(f.state.commands[1].id).not.toBe('retry')
      expect(f.state.commands.some(command => command.id === 'edit')).toBe(false)
      await queue.filter({ hasText: '带附件的待发内容' }).getByRole('button', { name: '编辑待发消息', exact: true }).click()
      await expect(f.page.getByLabel('消息内容')).toHaveValue('带附件的待发内容')
      await expect(f.page.getByRole('button', { name: '移除 note.txt', exact: true })).toBeVisible()
      await expect(queue.filter({ hasText: '带附件的待发内容' })).toHaveCount(0)
      await f.page.getByLabel('消息内容').fill('编辑后的附件消息')
      await f.page.getByRole('button', { name: '发送', exact: true }).click()
      const edited = queue.filter({ hasText: '编辑后的附件消息' })
      await expect(edited).toBeVisible()
      await expect(edited).toContainText('1 个附件')
      expect(f.state.commands).toHaveLength(2)
      const queueGeometry = () => queue.evaluateAll(elements => elements.map(element => {
        const rect = element.getBoundingClientRect()
        const style = getComputedStyle(element)
        return { id: element.dataset.commandId, top: rect.top, bottom: rect.bottom, height: rect.height, transform: style.transform }
      }))
      const updatingBoxes = await queueGeometry()
      await writeFile(path.join(output, 'queue-during-update.json'), JSON.stringify(updatingBoxes, null, 2))
      expect(updatingBoxes.every((box, index) => index === 0 || box.top >= updatingBoxes[index - 1].bottom), 'Reduced-motion queue updates must not overlap message cards').toBe(true)
      await expect.poll(async () => {
        const boxes = await queueGeometry()
        return boxes.every((box, index) => index === 0 || box.top >= boxes[index - 1].bottom)
      }, { message: 'Queued message cards must settle without overlap' }).toBe(true)
      await writeFile(path.join(output, 'queue-settled.json'), JSON.stringify(await queueGeometry(), null, 2))
      await f.screenshot('mobile-queue-controls')
      await edited.getByRole('button', { name: '立即引导', exact: true }).click()
      await expect.poll(() => f.state.commands.length).toBe(3)
      expect(f.state.commands[2]).toMatchObject({ text: '编辑后的附件消息', agent_id: agentId, session_id: 'parent', attachment_ids: ['already-uploaded'] })
      expect(f.state.requests.some(request => request.pathname === '/attachments' && request.method === 'POST')).toBe(false)
      await queue.filter({ hasText: '删除这个待发消息' }).getByRole('button', { name: '删除', exact: true }).click()
      await expect(queue).toHaveCount(0)
      expect(f.state.commands).toHaveLength(3)
    } finally { await f.close() }
  })
  await check('long queues scroll by touch without hiding the composer or sending a message', async () => {
    const seedQueue = Array.from({ length: 10 }, (_, index) => ({ payload: { id: `long-${index}`, agent_id: agentId, session_id: 'parent', action: 'send', text: `长队列消息 ${index + 1}`, attachment_ids: [], target_id: null }, files: [], attachments: [], state: 'queued' }))
    const f = await fixture({ width: 320, height: 640 }, { busy: true, seedQueue })
    try {
      const queue = f.page.getByRole('region', { name: '待发队列', exact: true })
      await expect(queue).toBeVisible()
      await expect(f.page.getByLabel('排队消息', { exact: true })).toHaveCount(seedQueue.length)
      const box = await queue.boundingBox()
      const header = await f.page.locator('.m-header').boundingBox()
      const composer = await f.page.locator('.m-composer').boundingBox()
      expect(box.y).toBeGreaterThanOrEqual(header.y + header.height)
      expect(box.y + box.height).toBeLessThanOrEqual(composer.y)
      expect(composer.y + composer.height).toBeLessThanOrEqual(640)
      expect(await queue.evaluate(element => element.scrollHeight > element.clientHeight)).toBe(true)
      const cdp = await f.context.newCDPSession(f.page)
      const x = box.x + box.width / 2
      const bottom = box.y + box.height - 20
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y: bottom }] })
      for (let index = 1; index <= 8; index++) await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: bottom - index * (box.height - 40) / 8 }] })
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      await expect.poll(() => queue.evaluate(element => element.scrollTop)).toBeGreaterThan(0)
      expect(f.state.commands).toHaveLength(0)
      await expect(f.page.getByLabel('消息内容')).toHaveValue('')
      await f.page.getByLabel('排队消息', { exact: true }).filter({ hasText: '长队列消息 10' }).getByRole('button', { name: '删除', exact: true }).click()
      await expect(f.page.getByLabel('排队消息', { exact: true })).toHaveCount(9)
      expect(f.state.commands).toHaveLength(0)
      await f.screenshot('narrow-scrollable-queue')
    } finally { await f.close() }
  })
  await check('offline image and quote survive reload and drain once after reconnect', async () => {
    const f = await fixture()
    try {
      await f.disconnect()
      await f.page.locator('.m-msg').first().click({ button: 'right' })
      await f.page.getByRole('menuitem', { name: '引用', exact: true }).click()
      await f.page.locator('input[type=file]').setInputFiles({ name: 'offline.png', mimeType: 'image/png', buffer: fixturePng })
      await f.page.getByRole('button', { name: '预览 offline.png', exact: true }).click()
      await expect(f.page.getByRole('dialog', { name: '图片预览', exact: true }).locator('img')).toHaveJSProperty('naturalWidth', 1)
      await f.page.getByRole('button', { name: '关闭图片', exact: true }).click()
      await f.page.getByLabel('消息内容').fill('离线带图片的回复')
      await f.page.getByRole('button', { name: '发送', exact: true }).click()
      await expect(f.page.getByLabel('排队消息', { exact: true })).toContainText('离线带图片的回复')
      expect(f.state.commands).toHaveLength(0)
      expect(f.state.uploads).toHaveLength(0)
      const stored = await f.page.evaluate(async () => {
        const db = await new Promise((resolve, reject) => { const request = indexedDB.open('astrorder:mobile', 1); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error) })
        const rows = await new Promise((resolve, reject) => { const request = db.transaction('outbox', 'readonly').objectStore('outbox').get('entries'); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error) })
        db.close()
        return { text: rows[0].payload.text, name: rows[0].files[0].name, file: rows[0].files[0] instanceof File, bytes: rows[0].files[0].size }
      })
      expect(stored).toMatchObject({ name: 'offline.png', file: true, bytes: fixturePng.length })
      expect(stored.text).toContain('> 工作区文件：')
      f.state.httpOffline = false
      await f.page.reload()
      await expect(f.page.getByLabel('排队消息', { exact: true })).toContainText('离线带图片的回复')
      expect(f.state.commands).toHaveLength(0)
      await f.screenshot('offline-attachment-after-reload')
      await f.reconnect()
      await expect.poll(() => f.state.commands.length).toBe(1)
      await expect(f.page.getByLabel('排队消息', { exact: true })).toHaveCount(0)
      expect(f.state.uploads).toHaveLength(1)
      expect(f.state.uploads[0].bytes).toBeGreaterThan(fixturePng.length)
      expect(f.state.commands[0]).toMatchObject({ agent_id: agentId, session_id: 'parent', action: 'send', text: stored.text, attachment_ids: [f.state.uploads[0].attachment.id] })
      await expect(f.page.locator('.m-user')).toHaveCount(1)
      await expect(f.page.locator('.m-user img')).toHaveJSProperty('naturalWidth', 1)
      await f.disconnect()
      await f.reconnect()
      await expect.poll(async () => (await f.readQueue()).length).toBe(0)
      expect(f.state.commands).toHaveLength(1)
      expect(f.state.uploads).toHaveLength(1)
      await writeFile(path.join(output, 'offline-attachment-evidence.json'), JSON.stringify({ stored, command: f.state.commands[0], uploadCount: f.state.uploads.length }, null, 2))
    } finally { await f.close() }
  })
  await check('direct image-only lost receipt is reconciled after reload without resubmission', async () => {
    const f = await fixture()
    try {
      f.state.ambiguousNext = true
      await f.page.locator('input[type=file]').setInputFiles({ name: 'direct.png', mimeType: 'image/png', buffer: fixturePng })
      await f.page.getByRole('button', { name: '发送', exact: true }).click()
      await expect(f.page.getByRole('button', { name: '核对发送结果', exact: true })).toBeVisible()
      expect(f.state.commands).toHaveLength(1)
      expect(f.state.uploads).toHaveLength(1)
      const queued = await f.readQueue()
      expect(queued).toHaveLength(1)
      expect(queued[0]).toMatchObject({ state: 'unknown', files: [], payload: { id: f.state.commands[0].id, text: '', attachment_ids: [f.state.uploads[0].attachment.id] } })
      await expect(f.page.locator('.m-user img')).toHaveJSProperty('naturalWidth', 1)
      await f.screenshot('direct-image-uncertain-receipt')
      await f.page.reload()
      await expect(f.page.getByLabel('已连接', { exact: true })).toBeVisible()
      await expect.poll(async () => (await f.readQueue()).length).toBe(0)
      await expect(f.page.locator('.m-user')).toHaveCount(1)
      expect(f.state.commands).toHaveLength(1)
      expect(f.state.uploads).toHaveLength(1)
      await f.page.locator('input[type=file]').setInputFiles({ name: 'direct.png', mimeType: 'image/png', buffer: fixturePng })
      await f.page.getByRole('button', { name: '发送', exact: true }).click()
      await expect.poll(() => f.state.commands.length).toBe(2)
      await expect(f.page.locator('.m-user')).toHaveCount(2)
      expect(new Set(f.state.commands.map(command => command.id)).size).toBe(2)
      expect(f.state.uploads).toHaveLength(2)
      await writeFile(path.join(output, 'direct-image-receipt-evidence.json'), JSON.stringify({ persisted: queued[0], commands: f.state.commands, uploadCount: f.state.uploads.length }, null, 2))
    } finally { await f.close() }
  })
  await check('event reconnect resumes cursor and wake events retain one live connection', async () => {
    const f = await fixture()
    try {
      f.state.sessions[0].title = '已收到事件游标'
      f.state.events.at(-1).socket.send(JSON.stringify({ id: 'cursor-probe', cursor: 17, type: 'session.upsert', agent_id: agentId, session_id: 'parent', data: f.state.sessions[0] }))
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['已收到事件游标'])
      await f.disconnect()
      const before = f.state.events.length
      await f.reconnect()
      await expect.poll(() => f.state.events.length).toBe(before + 1)
      expect(f.state.events.at(-1).after).toBe(17)
      await f.page.evaluate(() => { window.dispatchEvent(new Event('online')); window.dispatchEvent(new Event('online')); document.dispatchEvent(new Event('visibilitychange')) })
      await f.page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
      expect(f.state.events).toHaveLength(before + 1)
      expect(f.state.commands).toHaveLength(0)
      await writeFile(path.join(output, 'event-reconnect-evidence.json'), JSON.stringify(f.state.events.map(({ after, closed }) => ({ after, closed })), null, 2))
    } finally { await f.close() }
  })
  await check('touch message actions and drawer swipes preserve session drafts', async () => {
    const f = await fixture()
    try {
      await f.context.grantPermissions(['clipboard-read', 'clipboard-write'])
      const cdp = await f.context.newCDPSession(f.page)
      const swipe = async (x, y, dx, dy) => {
        await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
        for (let step = 1; step <= 10; step++) {
          await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x + dx * step / 10, y: y + dy * step / 10 }] })
          await new Promise(resolve => setTimeout(resolve, 16))
        }
        await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      }
      const message = f.page.locator('.m-msg').first()
      const holdMessage = async () => {
        const box = await message.boundingBox()
        await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: box.x + 18, y: box.y + 16 }] })
        await expect(f.page.getByRole('menu', { name: '消息操作', exact: true })).toBeVisible()
        await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      }
      const text = await message.locator('.m-bubble').innerText()
      await holdMessage()
      const menuBox = await f.page.getByRole('menu', { name: '消息操作', exact: true }).boundingBox()
      expect(menuBox.x).toBeGreaterThanOrEqual(0)
      expect(menuBox.x + menuBox.width).toBeLessThanOrEqual(390)
      await f.page.getByRole('menuitem', { name: '复制', exact: true }).tap()
      await expect.poll(() => f.page.evaluate(() => navigator.clipboard.readText())).toBe('工作区文件：[notes.md](notes.md)')
      await holdMessage()
      await f.page.getByRole('menuitem', { name: '选择', exact: true }).tap()
      await expect.poll(() => f.page.evaluate(() => window.getSelection()?.toString())).toBe(text)
      expect(await message.locator('.m-bubble').evaluate(element => getComputedStyle(element).userSelect)).toBe('text')
      await f.screenshot('touch-message-selection')
      await f.page.getByLabel('消息内容').tap()
      await expect(message.locator('.m-bubble')).not.toHaveClass(/m-select-text/)
      await holdMessage()
      await writeFile(path.join(output, 'touch-menu-focus.json'), JSON.stringify(await f.page.evaluate(() => ({ tag: document.activeElement?.tagName, role: document.activeElement?.getAttribute('role'), className: document.activeElement?.className })), null, 2))
      await f.page.keyboard.press('Escape')
      await expect(f.page.getByRole('menu', { name: '消息操作', exact: true })).toHaveCount(0)
      await holdMessage()
      await f.page.getByLabel('消息内容').tap()
      await expect(f.page.getByRole('menu', { name: '消息操作', exact: true })).toHaveCount(0)
      await holdMessage()
      await f.page.getByRole('menuitem', { name: '引用', exact: true }).tap()
      await expect(f.page.locator('.m-quote')).toContainText('工作区文件：[notes.md](notes.md)')
      await f.page.getByLabel('消息内容').fill('保留父会话草稿')
      await f.page.getByLabel('消息内容').blur()
      const log = await f.page.getByRole('log').boundingBox()
      await swipe(300, log.y + 45, -165, 45)
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['子代理会话'])
      await expect(f.page.getByLabel('消息内容')).toHaveValue('')
      expect(new URL(f.page.url()).searchParams.get('agent_id')).toBe(agentId)
      const childLog = await f.page.getByRole('log').boundingBox()
      await swipe(100, childLog.y + 55, 165, -40)
      await expect(f.page.locator('.m-card-head h1')).toHaveText(['移动端完整功能验收'])
      await expect(f.page.getByLabel('消息内容')).toHaveValue('保留父会话草稿')
      await expect(f.page.locator('.m-quote')).toContainText('工作区文件：[notes.md](notes.md)')
      await swipe(8, log.y + 55, 140, 8)
      await expect(f.page.getByRole('dialog', { name: '会话列表', exact: true })).toBeVisible()
      await expect(f.page.locator('.m-card-head h1')).toHaveText('移动端完整功能验收')
      await f.screenshot('touch-session-drawer')
      await swipe(280, 88, -170, 4)
      await expect(f.page.getByRole('dialog', { name: '会话列表', exact: true })).toHaveCount(0)
      await expect(f.page.getByLabel('消息内容')).toHaveValue('保留父会话草稿')
      expect(f.state.commands).toHaveLength(0)
      expect(f.state.uploads).toHaveLength(0)
      await writeFile(path.join(output, 'touch-navigation-evidence.json'), JSON.stringify({ input: 'CDP touchStart/touchMove/touchEnd in headless Chromium', draft: await f.page.getByLabel('消息内容').inputValue(), selectedSession: new URL(f.page.url()).pathname, commandCount: f.state.commands.length }, null, 2))
    } finally { await f.close() }
  })
  await check('phone file search tree and Git branch operations remain inside the viewport', async () => {
    const f = await fixture({ width: 320, height: 740 }, { theme: 'light' })
    try {
      await f.menu('工作区')
      await f.page.getByRole('button', { name: '快速打开文件', exact: true }).click()
      const search = f.page.getByRole('dialog', { name: '快速打开文件', exact: true })
      await expect(search.getByRole('button', { name: /notes.md/ })).toBeVisible()
      const box = await f.page.locator('.quick-open-card').boundingBox()
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(320)
      await f.screenshot('narrow-file-search')
      await f.page.getByLabel('搜索文件名').press('Escape')
      await expect(search).toHaveCount(0)
      await expect(f.page.getByRole('dialog', { name: '会话工作区', exact: true })).toBeVisible()
      await f.page.getByRole('button', { name: '切换或创建 Git 分支', exact: true }).click()
      await f.page.getByRole('menuitem', { name: 'feature/next', exact: true }).click()
      await expect.poll(() => f.state.branch).toBe('feature/next')
      await expect(f.page.getByRole('button', { name: '切换或创建 Git 分支', exact: true })).toContainText('feature/next')
      await f.page.getByRole('button', { name: '新建面板', exact: true }).click()
      await f.page.getByRole('button', { name: /^文件/ }).click()
      await expect(f.page.locator('.filetree-viewer-pane')).toContainText('notes.md')
      expect(f.state.requests.find(item => item.pathname === '/files/tree')).toMatchObject({ authenticated: true, query: { session_id: 'parent' } })
      await f.screenshot('narrow-file-tree')
    } finally { await f.close() }
  })
} finally {
  await browser?.close()
  await new Promise(resolve => server.close(resolve))
  await writeFile(path.join(output, 'results.json'), JSON.stringify(summary(), null, 2))
}
console.log(JSON.stringify({ passed: summary().passed, failed: summary().failed, output }))
if (results.some(result => result.status !== 'passed')) process.exitCode = 1
