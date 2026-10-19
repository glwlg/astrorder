import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, authenticatedApiFetch } from './client'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('contract HTTP client', () => {
  it('maps chat fork options to the strict App request contract', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
    await api.forkSession('parent', { agent_id: 'a', worktree: false, target_message_id: 'antigravity:parent:1' })
    expect(JSON.parse(fetchMock.mock.calls[0][1]?.body as string)).toEqual({
      agent_id: 'a', create_worktree: false, target_message_id: 'antigravity:parent:1',
    })
    await api.forkSession('parent', { agent_id: 'a', worktree: true, branch_name: 'child' })
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toEqual({
      agent_id: 'a', create_worktree: true, worktree_branch: 'child',
    })
  })

  it('sends auth tokens only in the POST body and uses cookies for later requests', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ authenticated: true }), { status: 200 }),
    )

    await api.login('test-secret')
    await api.getAuthSession()

    const loginRequest = fetchMock.mock.calls[0]
    const sessionRequest = fetchMock.mock.calls[1]
    expect(String(loginRequest[0])).not.toContain('test-secret')
    expect(String(sessionRequest[0])).not.toContain('test-secret')
    expect((loginRequest[1]?.body as string)).toContain('test-secret')
    expect(loginRequest[1]?.credentials).toBe('include')
    expect(sessionRequest[1]?.credentials).toBe('include')
  })

  it('waits for native file resolution before uploading the original path', async () => {
    let resolvePath!: (path: string) => void
    const getPathForFile = vi.fn(() => new Promise<string>(resolve => { resolvePath = resolve }))
    const desktopWindow = window as unknown as { astrorderDesktop?: { getPathForFile: typeof getPathForFile } }
    desktopWindow.astrorderDesktop = { getPathForFile }
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
    try {
      const file = new File(['content'], '报告;图.txt', { type: 'text/plain' })
      const uploaded = api.uploadAttachment(file)
      expect(fetchMock).not.toHaveBeenCalled()
      resolvePath('C:/中文;目录/报告;图.txt')
      await uploaded
      const form = fetchMock.mock.calls[0][1]?.body as FormData
      expect(form.get('source_path')).toBe('C:/中文;目录/报告;图.txt')
      expect(getPathForFile).toHaveBeenCalledWith(file)
    } finally {
      delete desktopWindow.astrorderDesktop
    }
  })

  it('materializes Wails disk files before multipart upload', async () => {
    const originalLocation = window.location
    Object.defineProperty(window, 'location', { value: new URL('http://wails.localhost/'), writable: true })
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{}', { status: 200 }))
    const file = new File(['content'], 'image.png', { type: 'image/png' })
    const arrayBuffer = vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3]).buffer)
    Object.defineProperty(file, 'arrayBuffer', { value: arrayBuffer })
    try {
      await api.uploadAttachment(file)
      expect(arrayBuffer).toHaveBeenCalledOnce()
      const form = fetchMock.mock.calls[0][1]?.body as FormData
      const uploaded = form.get('file') as File
      expect(uploaded.name).toBe('image.png')
      expect(uploaded.type).toBe('image/png')
      expect(uploaded.size).toBe(3)
    } finally {
      Object.defineProperty(window, 'location', { value: originalLocation, writable: true })
    }
  })

  it('keeps browser authentication on same-origin file requests only', async () => {
    const credential = crypto.randomUUID()
    localStorage.setItem('astrorder:token', credential)
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('file', { status: 200 }))
    try {
      await authenticatedApiFetch('/api/v1/files/raw?path=notes.md', { headers: { 'X-Viewer': 'mobile' } })
      await authenticatedApiFetch(new URL('/api/v1/files/raw?path=flow.mmd', window.location.href).href)
      await authenticatedApiFetch('https://external.invalid/notes.md')
      await authenticatedApiFetch('//external.invalid/flow.mmd')
      for (const [, init] of fetchMock.mock.calls.slice(0, 2)) {
        expect(new Headers(init?.headers).get('Authorization') === `Bearer ${credential}`).toBe(true)
        expect(init?.credentials).toBe('include')
      }
      expect(new Headers(fetchMock.mock.calls[0][1]?.headers).get('X-Viewer')).toBe('mobile')
      for (const [, init] of fetchMock.mock.calls.slice(2)) {
        expect(new Headers(init?.headers).has('Authorization')).toBe(false)
        expect(init?.credentials).toBe('omit')
      }
    } finally {
      localStorage.removeItem('astrorder:token')
    }
  })

  it('throws the server detail without exposing response internals', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ detail: '认证失败' }), { status: 401 }),
    )

    await expect(api.getAgents()).rejects.toMatchObject({ status: 401, detail: '认证失败' })
  })

  it('calls kernel management status and operation endpoints according to API contract', async () => {
    const statusPayload = {
      app: { state: 'running', healthy: true, process: { pid: 1001, creation_time: '2026-10-17T08:00:00Z' } },
      daemon: { state: 'running', active_sessions: { 'sess-1': {} }, process: { pid: 1002, creation_time: '2026-10-17T08:00:01Z' } },
      update: { update_available: true },
    }
    const createdPayload = {
      id: 'op-42',
      status: 'queued',
      target: 'daemon',
      action: 'update',
    }
    const operationPayload = {
      id: 'op-42',
      status: 'running',
      target: 'daemon',
      action: 'update',
    }

    const fetchMock = vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(new Response(JSON.stringify(statusPayload), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(createdPayload), { status: 202 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(operationPayload), { status: 200 }))

    const status = await (api as any).getKernelStatus()
    expect(status).toEqual(statusPayload)
    expect(String(fetchMock.mock.calls[0][0])).toBe('/api/v1/services/kernels/status')

    const op = await (api as any).createKernelOperation({ target: 'daemon', action: 'update', confirm_active: true })
    expect(op).toEqual(createdPayload)
    expect(String(fetchMock.mock.calls[1][0])).toBe('/api/v1/services/kernels/operations')
    expect(JSON.parse(fetchMock.mock.calls[1][1]?.body as string)).toEqual({ target: 'daemon', action: 'update', confirm_active: true })

    const detail = await (api as any).getKernelOperation('op-42')
    expect(detail).toEqual(operationPayload)
    expect(String(fetchMock.mock.calls[2][0])).toBe('/api/v1/services/kernels/operations/op-42')
  })
})
