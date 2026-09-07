import { afterEach, describe, expect, it, vi } from 'vitest'
import { api } from './client'

afterEach(() => {
  vi.restoreAllMocks()
})

describe('contract HTTP client', () => {
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

  it('throws the server detail without exposing response internals', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ detail: '认证失败' }), { status: 401 }),
    )

    await expect(api.getAgents()).rejects.toMatchObject({ status: 401, detail: '认证失败' })
  })
})
