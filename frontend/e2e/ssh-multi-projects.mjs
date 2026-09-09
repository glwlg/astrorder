import { chromium } from '@playwright/test'
import assert from 'node:assert/strict'

const executablePath = 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const fixtureToken = crypto.randomUUID()
const baseUrl = process.argv[2] || process.env.ASTRORDER_E2E_BASE_URL || 'http://127.0.0.1:30001'

function connectionsPayload(items) {
  return {
    local: {
      kind: 'hermes', state: 'discovered', available: true, agent_id: null, source_id: 'local-source',
      profile_name: 'default', runtime_id: null, session_id: null, version: 'fixture', detail: 'discovered',
    },
    ssh: { items, state: items[0]?.state ?? 'unconfigured', settings: items[0]?.settings ?? null, detail: items[0]?.detail ?? null },
  }
}

function makeConnection(id, name, host) {
  return {
    id, display_name: name, profile_name: 'default', state: 'configured', detail: 'saved', remote_os: null,
    agent_id: null, runtime_id: null,
    settings: { host, port: 22, user: 'fixture', ssh_config_alias: null, identity_file: null, hermes_path: null, workspace: null },
  }
}

async function runViewport(viewport, label) {
  let authenticated = false
  const items = [makeConnection('ssh-a', 'Connection A', 'a.example.test'), makeConnection('ssh-b', 'Connection B', 'b.example.test')]
  const sessions = [
    { id: 'session-a1', agent_id: 'runtime-a', title: 'same title', workspace: '/work/alpha', status: 'idle', updated_at: '2026-09-07T00:00:00Z', source_id: 'source-a', connection_id: 'ssh-a', source_session_id: 'native-a1', project_id: 'project-alpha', project_name: 'Project Alpha', history_state: 'available', control_state: 'readonly' },
    { id: 'session-a2', agent_id: 'runtime-a', title: 'same title', workspace: '/work/alpha', status: 'idle', updated_at: '2026-09-07T00:00:01Z', source_id: 'source-a', connection_id: 'ssh-a', source_session_id: 'native-a2', project_id: 'project-alpha', project_name: 'Project Alpha', history_state: 'available', control_state: 'readonly' },
    { id: 'session-b1', agent_id: 'runtime-b', title: 'same title', workspace: '/work/alpha', status: 'idle', updated_at: '2026-09-07T00:00:02Z', source_id: 'source-b', connection_id: 'ssh-b', source_session_id: 'native-b1', project_id: 'project-alpha', project_name: 'Project Alpha', history_state: 'available', control_state: 'readonly' },
  ]
  const projects = [
    { id: 'project-row-a', source_id: 'source-a', connection_id: 'ssh-a', agent_id: 'runtime-a', project_id: 'project-alpha', project_name: 'Project Alpha', workspace: '/work/alpha', session_count: 2, updated_at: '2026-09-07T00:00:01Z' },
    { id: 'project-row-b', source_id: 'source-b', connection_id: 'ssh-b', agent_id: 'runtime-b', project_id: 'project-alpha', project_name: 'Project Alpha', workspace: '/work/alpha', session_count: 1, updated_at: '2026-09-07T00:00:02Z' },
    { id: 'project-row-empty', source_id: 'source-a', connection_id: 'ssh-a', agent_id: 'runtime-a', project_id: 'project-empty', project_name: 'Empty Project', workspace: '/work/empty', session_count: 0, updated_at: '2026-09-06T23:00:00Z' },
  ]
  const agents = [
    { id: 'runtime-a', kind: 'hermes', name: 'Hermes A', status: 'ready', capabilities: ['chat', 'queue', 'attachments', 'stop'], limitation: null, source_id: 'source-a', connection_id: 'ssh-a', profile_name: 'default', runtime_id: 'runtime-a', control_state: 'owned' },
    { id: 'runtime-b', kind: 'hermes', name: 'Hermes B', status: 'ready', capabilities: ['chat', 'queue', 'attachments', 'stop'], limitation: null, source_id: 'source-b', connection_id: 'ssh-b', profile_name: 'default', runtime_id: 'runtime-b', control_state: 'owned' },
  ]
  const browser = await chromium.launch({ executablePath, headless: true })
  const page = await browser.newPage({ viewport })
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'mediaDevices', {
      configurable: true,
      value: { getUserMedia: async () => { throw new DOMException('permission denied', 'NotAllowedError') } },
    })
  })
  try {
    await page.route('**/api/v1/**', async route => {
      const request = route.request()
      const url = new URL(request.url())
      const path = url.pathname
      if (path.endsWith('/auth/session')) {
        if (request.method() === 'POST') authenticated = true
        return route.fulfill({ json: { authenticated } })
      }
      if (path.endsWith('/bootstrap')) return route.fulfill({ json: { protocol_version: 1, agents, projects, sessions, cursor: 0 } })
      if (path.endsWith('/connections') && request.method() === 'GET') return route.fulfill({ json: connectionsPayload(items) })
      if (path.endsWith('/connections/ssh') && request.method() === 'PUT') {
        const body = JSON.parse(request.postData() || '{}')
        const created = makeConnection('ssh-new', body.display_name || 'Connection New', body.host || 'new.example.test')
        created.settings = { ...created.settings, ...body }
        items.push(created)
        return route.fulfill({ json: created })
      }
      const match = path.match(/\/connections\/ssh\/([^/]+)/)
      if (match) {
        const current = items.find(item => item.id === decodeURIComponent(match[1]))
        if (path.endsWith('/connect')) {
          if (current) {
            current.state = 'connected'
            current.detail = 'connected through native fixture'
          }
          return route.fulfill({ json: connectionsPayload(items) })
        }
        if (path.endsWith('/disconnect')) {
          if (current) current.state = 'disconnected'
          return route.fulfill({ json: connectionsPayload(items) })
        }
        if (path.endsWith('/test')) {
          if (current) current.state = 'validated'
          return route.fulfill({ json: current })
        }
        if (request.method() === 'PUT') return route.fulfill({ json: current })
      }
      if (path.endsWith('/runtime')) return route.fulfill({ json: { items: [] } })
      if (path.includes('/messages')) {
        const match = path.match(/\/sessions\/([^/]+)\/messages/)
        const sessionId = match ? decodeURIComponent(match[1]) : 'session-a1'
        const agentId = url.searchParams.get('agent_id') || 'runtime-a'
        return route.fulfill({ json: { items: [{ id: `runtime-${sessionId}`, session_id: sessionId, agent_id: agentId, role: 'tool', kind: 'tool', text: '已读取公开运行摘要', attachments: [], created_at: '2026-09-07T00:00:03Z', command_id: null, tool: { name: '公开状态', branch: 'feature/demo', changed_files: ['README.md', 'src/app.ts'], background_tasks: { running: 1, total: 2 }, todo: { completed: 1, total: 3 }, subagents: { done: 1, total: 2 } } }], next_cursor: null } })
      }
      if (path.endsWith('/commands') && request.method() === 'GET') {
        const match = path.match(/\/sessions\/([^/]+)\/commands/)
        const sessionId = match ? decodeURIComponent(match[1]) : 'session-a1'
        const agentId = url.searchParams.get('agent_id') || 'runtime-a'
        return route.fulfill({ json: { items: [{ id: `queued-${sessionId}`, session_id: sessionId, agent_id: agentId, action: 'enqueue', state: 'queued', text: '已有排队任务', attachments: [], created_at: '2026-09-07T00:00:04Z', error: null, target_id: null }] } })
      }
      if (path.endsWith('/commands') && request.method() === 'POST') {
        const body = JSON.parse(request.postData() || '{}')
        return route.fulfill({ json: { id: body.id, session_id: body.session_id, agent_id: body.agent_id, action: body.action, state: 'queued', text: body.text || '', attachments: [], created_at: '2026-09-07T00:00:05Z', error: null, target_id: body.target_id || null } })
      }
      return route.fulfill({ json: { items: [] } })
    })
    await page.routeWebSocket('**/ws/**', () => {})

    await page.goto(`${baseUrl}/agents`, { waitUntil: 'networkidle' })
    await page.getByLabel('访问令牌').fill(fixtureToken)
    await page.getByRole('button', { name: '建立会话' }).click()
    await page.getByRole('heading', { name: '连接管理' }).waitFor()
    await page.getByRole('button', { name: '添加连接' }).click()
    await page.getByLabel('连接显示名').first().fill('Connection New')
    await page.getByLabel('SSH 主机').first().fill('new.example.test')
    await page.getByRole('button', { name: '保存 SSH 配置' }).first().click()
    await page.getByRole('button', { name: /Connection New，未连接/ }).waitFor()
    for (const name of ['Connection A', 'Connection B', 'Connection New']) {
      await page.getByRole('button', { name: new RegExp(`${name}，未连接`) }).click()
      await page.getByRole('button', { name: '连接远程 Hermes' }).click()
      await page.getByRole('button', { name: '关闭详情' }).click()
      await page.getByRole('button', { name: new RegExp(`${name}，已连接`) }).waitFor()
    }

    await page.goto(`${baseUrl}/chat`, { waitUntil: 'networkidle' })
    const openRail = async () => {
      if (viewport.width < 600) await page.getByLabel('打开导航').click()
    }
    await openRail()
    const projectRows = page.locator('[data-project-key]')
    assert.equal(await projectRows.count(), 3, `${label} must include two isolated source projects and one zero-session project`)
    await page.getByRole('region', { name: 'Empty Project Hermes A' }).getByRole('button', { name: /0 个会话/ }).waitFor()
    const groupA = page.getByRole('region', { name: 'Project Alpha Hermes A' })
    await groupA.getByRole('button', { name: /same title/ }).first().click()
    await page.waitForURL(/\/chat\/session-a[12]/)
    await page.getByLabel('运行摘要').waitFor()
    await page.getByText('feature/demo', { exact: true }).waitFor()
    await page.getByRole('button', { name: '展开运行详情' }).click()
    await page.getByLabel('公开运行日志').waitFor()
    await page.getByLabel('消息内容').fill('浏览器队列验收')
    await page.getByRole('button', { name: '排队发送' }).click()
    await page.getByText(/个排队/).waitFor()
    await page.getByRole('button', { name: '语音输入' }).click()
    const voiceDialog = page.getByRole('dialog').last()
    await voiceDialog.getByRole('button', { name: '开始录音' }).click()
    await voiceDialog.getByText('麦克风权限被拒绝或未提供。').waitFor()
    await voiceDialog.getByRole('button', { name: '取消' }).click()
    await page.goto(`${baseUrl}/monitor`, { waitUntil: 'networkidle' })
    await page.getByRole('heading', { name: '监控室' }).waitFor()
    await page.locator('.monitor-queue').waitFor()
    await page.goto(`${baseUrl}/chat`, { waitUntil: 'networkidle' })
    await openRail()
    const groupB = page.getByRole('region', { name: 'Project Alpha Hermes B' })
    await groupB.getByRole('button', { name: /same title/ }).first().click()
    await page.waitForURL(/\/chat\/session-b1/)
    const dimensions = await page.evaluate(() => ({ client: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }))
    assert(dimensions.scroll <= dimensions.client, `${label} viewport has horizontal overflow: ${JSON.stringify(dimensions)}`)
    await page.screenshot({ path: `.runtime/ssh-multi-projects-${label}.png`, fullPage: true })
    console.log(`${label} multi-connection/project clicks passed`)
  } finally {
    await browser.close()
  }
}

await runViewport({ width: 1440, height: 1000 }, 'desktop')
await runViewport({ width: 390, height: 844 }, 'mobile')
