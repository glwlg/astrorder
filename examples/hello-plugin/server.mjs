import http from 'node:http'

const PLUGIN_ID = process.env.ASTRORDER_PLUGIN_ID || 'hello-plugin'
const HOST_URL = (process.env.ASTRORDER_HOST_URL || '').replace(/\/+$/, '')
const HOST_TOKEN = process.env.ASTRORDER_HOST_TOKEN || ''
const STARTED_AT = Date.now()

function parseBindAddress() {
  const addr = (process.env.ASTRORDER_PLUGIN_ADDR || '').trim()
  if (addr) {
    const idx = addr.lastIndexOf(':')
    if (idx > 0) {
      const host = addr.slice(0, idx) || '127.0.0.1'
      const port = Number.parseInt(addr.slice(idx + 1), 10)
      if (Number.isFinite(port) && port > 0) {
        return { host, port }
      }
    }
  }
  const envPort = Number.parseInt(process.env.ASTRORDER_PLUGIN_PORT || '47890', 10)
  return { host: '127.0.0.1', port: Number.isFinite(envPort) && envPort > 0 ? envPort : 47890 }
}

function sendJson(res, statusCode, payload) {
  const body = JSON.stringify(payload)
  res.writeHead(statusCode, {
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
  })
  res.end(body)
}

async function readJsonBody(req) {
  const chunks = []
  let size = 0
  for await (const chunk of req) {
    size += chunk.length
    if (size > 1024 * 1024) {
      throw new Error('request body too large')
    }
    chunks.push(chunk)
  }
  if (chunks.length === 0) return {}
  const text = Buffer.concat(chunks).toString('utf8').trim()
  if (!text) return {}
  return JSON.parse(text)
}

async function hostApi(path, init = {}) {
  if (!HOST_URL || !HOST_TOKEN) {
    return { ok: false, status: 0, data: null, error: 'host api unavailable' }
  }
  try {
    const headers = {
      Authorization: `Bearer ${HOST_TOKEN}`,
      'X-Astrorder-Host-Token': HOST_TOKEN,
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init.headers || {}),
    }
    const resp = await fetch(`${HOST_URL}${path}`, { ...init, headers })
    const text = await resp.text()
    let data = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      data = text
    }
    return { ok: resp.ok, status: resp.status, data, error: resp.ok ? null : text }
  } catch (err) {
    return { ok: false, status: 0, data: null, error: err instanceof Error ? err.message : String(err) }
  }
}

async function fetchWorkspaceGitInfo({ workspace, connection_id, session_id }) {
  if (!workspace) return null
  const q = new URLSearchParams()
  q.set('workspace', workspace)
  if (connection_id) q.set('connection_id', connection_id)
  if (session_id) q.set('session_id', session_id)
  const resp = await hostApi(`/api/v1/plugin-host/workspace/git-info?${q.toString()}`)
  if (!resp.ok || !resp.data) return null
  return resp.data.git || resp.data
}

async function readKV() {
  const resp = await hostApi('/api/v1/plugin-host/storage/kv')
  if (!resp.ok || !resp.data || typeof resp.data !== 'object') return {}
  return resp.data.values && typeof resp.data.values === 'object' ? resp.data.values : resp.data
}

async function writeKV(values) {
  return hostApi('/api/v1/plugin-host/storage/kv', {
    method: 'PUT',
    body: JSON.stringify({ values }),
  })
}

async function readSecretsMeta() {
  const resp = await hostApi('/api/v1/plugin-host/storage/secrets')
  if (!resp.ok || !resp.data || typeof resp.data !== 'object') {
    return { api_token_set: false }
  }
  const secrets = resp.data.secrets && typeof resp.data.secrets === 'object' ? resp.data.secrets : resp.data
  return { api_token_set: Boolean(secrets.api_token) }
}

async function writeSecrets(secrets) {
  return hostApi('/api/v1/plugin-host/storage/secrets', {
    method: 'PUT',
    body: JSON.stringify({ secrets }),
  })
}

const server = http.createServer(async (req, res) => {
  const reqUrl = new URL(req.url || '/', 'http://127.0.0.1')
  const pathname = reqUrl.pathname.replace(/\/+$/, '') || '/'
  const method = (req.method || 'GET').toUpperCase()

  try {
    if (method === 'GET' && pathname === '/health') {
      return sendJson(res, 200, {
        ok: true,
        plugin_id: PLUGIN_ID,
        uptime_sec: Math.floor((Date.now() - STARTED_AT) / 1000),
        host_connected: Boolean(HOST_URL && HOST_TOKEN),
      })
    }

    if (method === 'GET' && pathname === '/status-pill') {
      const workspace = reqUrl.searchParams.get('workspace') || ''
      const connectionId = reqUrl.searchParams.get('connection_id') || ''
      const sessionId = reqUrl.searchParams.get('session_id') || ''
      const ref = reqUrl.searchParams.get('ref') || ''
      const [kv, git] = await Promise.all([
        readKV(),
        fetchWorkspaceGitInfo({ workspace, connection_id: connectionId, session_id: sessionId }),
      ])
      const greeting = typeof kv.greeting === 'string' && kv.greeting.trim() ? kv.greeting.trim() : 'Hello Ext'
      const branch = git?.branch || ref || ''
      const repoLabel = git?.project_path || ''
      return sendJson(res, 200, {
        visible: true,
        label: greeting,
        sublabel: branch ? `@${branch}` : undefined,
        tone: 'success',
        dot_color: '#7c3aed',
        pulsing: false,
        tooltip: repoLabel
          ? `${greeting} · ${repoLabel}${branch ? ` (${branch})` : ''} — 点击打开 Hello Extension 面板`
          : `${greeting} — 点击打开 Hello Extension 面板`,
        panel_params: {
          ...(branch ? { branch } : {}),
          ...(repoLabel ? { project: repoLabel } : {}),
        },
      })
    }

    if (method === 'GET' && pathname === '/state') {
      const workspace = reqUrl.searchParams.get('workspace') || ''
      const connectionId = reqUrl.searchParams.get('connection_id') || ''
      const sessionId = reqUrl.searchParams.get('session_id') || ''
      const [kv, secretsMeta, git] = await Promise.all([
        readKV(),
        readSecretsMeta(),
        fetchWorkspaceGitInfo({ workspace, connection_id: connectionId, session_id: sessionId }),
      ])
      return sendJson(res, 200, {
        plugin_id: PLUGIN_ID,
        uptime_sec: Math.floor((Date.now() - STARTED_AT) / 1000),
        settings: {
          greeting: typeof kv.greeting === 'string' && kv.greeting.trim() ? kv.greeting : 'Hello Ext',
          emit_demo_signal: Boolean(kv.emit_demo_signal),
          api_token_set: secretsMeta.api_token_set,
        },
        git,
      })
    }

    if ((method === 'POST' || method === 'PUT') && pathname === '/settings') {
      const body = await readJsonBody(req)
      const nextKV = {
        greeting: typeof body.greeting === 'string' && body.greeting.trim() ? body.greeting.trim() : 'Hello Ext',
        emit_demo_signal: Boolean(body.emit_demo_signal),
      }
      const kvRes = await writeKV(nextKV)
      if (!kvRes.ok) {
        return sendJson(res, 502, { ok: false, detail: kvRes.error || 'failed to save kv settings' })
      }
      if (typeof body.api_token === 'string' && body.api_token.trim()) {
        const secRes = await writeSecrets({ api_token: body.api_token.trim() })
        if (!secRes.ok) {
          return sendJson(res, 502, { ok: false, detail: secRes.error || 'failed to save secret' })
        }
      }
      const secretsMeta = await readSecretsMeta()
      return sendJson(res, 200, {
        ok: true,
        settings: {
          ...nextKV,
          api_token_set: secretsMeta.api_token_set,
        },
      })
    }

    if (method === 'POST' && pathname === '/agent/echo') {
      const body = await readJsonBody(req)
      const [kv, git] = await Promise.all([
        readKV(),
        fetchWorkspaceGitInfo({
          workspace: typeof body.workspace === 'string' ? body.workspace : '',
          connection_id: typeof body.connection_id === 'string' ? body.connection_id : '',
          session_id: typeof body.session_id === 'string' ? body.session_id : '',
        }),
      ])
      return sendJson(res, 200, {
        ok: true,
        plugin_id: PLUGIN_ID,
        greeting: typeof kv.greeting === 'string' && kv.greeting.trim() ? kv.greeting.trim() : 'Hello Ext',
        echo: typeof body.message === 'string' && body.message.trim() ? body.message.trim() : 'Hello from third-party Astrorder plugin!',
        git,
        timestamp: new Date().toISOString(),
      })
    }

    if ((method === 'POST' || method === 'GET') && pathname === '/signals/failures') {
      const body = method === 'POST' ? await readJsonBody(req) : {}
      const workspace = typeof body.workspace === 'string' ? body.workspace : reqUrl.searchParams.get('workspace') || ''
      const kv = await readKV()
      if (!kv.emit_demo_signal) {
        return sendJson(res, 200, { items: [] })
      }
      return sendJson(res, 200, {
        items: [
          {
            source_plugin: PLUGIN_ID,
            kind: 'hello_demo',
            title: 'Hello Extension 示例告警信号',
            detail: workspace
              ? `工作区 ${workspace} 已开启示例信号（可在插件配置抽屉中关闭）。`
              : 'Hello Extension 已开启示例信号。',
            severity: 'warning',
            timestamp: new Date().toISOString(),
          },
        ],
      })
    }

    return sendJson(res, 404, { detail: `route not found: ${method} ${pathname}` })
  } catch (err) {
    return sendJson(res, 500, { detail: err instanceof Error ? err.message : String(err) })
  }
})

const { host, port } = parseBindAddress()
server.listen(port, host, () => {
  process.stdout.write(`[${PLUGIN_ID}] listening on http://${host}:${port}\n`)
})

function shutdown() {
  server.close(() => process.exit(0))
  setTimeout(() => process.exit(0), 2000).unref()
}

process.on('SIGTERM', shutdown)
process.on('SIGINT', shutdown)
