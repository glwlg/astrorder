import type {
  Agent,
  Attachment,
  AuthSession,
  BootstrapPayload,
  Command,
  CommandPayload,
  RuntimePayload,
  Session,
} from '../domain/types'

const API_PREFIX = '/api/v1'

export class ApiError extends Error {
  readonly status: number
  readonly detail: string

  constructor(status: number, detail: string) {
    super(detail)
    this.name = 'ApiError'
    this.status = status
    this.detail = detail
  }
}

async function parseResponse(response: Response): Promise<unknown> {
  if (response.status === 204) return undefined
  try {
    const raw = await response.text()
    if (!raw) return undefined
    try {
      return JSON.parse(raw) as unknown
    } catch {
      return raw
    }
  } catch {
    return undefined
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  const response = await fetch(`${API_PREFIX}${path}`, {
    ...init,
    credentials: 'include',
    headers,
  })
  const payload = await parseResponse(response)
  if (!response.ok) {
    const detail =
      typeof payload === 'object' && payload !== null && 'detail' in payload && typeof payload.detail === 'string'
        ? payload.detail
        : `请求失败（${response.status}）`
    throw new ApiError(response.status, detail)
  }
  return payload as T
}

function jsonRequest<T>(path: string, body: unknown, method = 'POST'): Promise<T> {
  return request<T>(path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}

function sessionPath(sessionId: string): string {
  return `/sessions/${encodeURIComponent(sessionId)}`
}

export const api = {
  getAuthSession: () => request<AuthSession>('/auth/session'),
  login: (token: string) => jsonRequest<AuthSession>('/auth/session', { token }),
  logout: () => request<void>('/auth/session', { method: 'DELETE' }),
  getBootstrap: () => request<BootstrapPayload>('/bootstrap'),
  getAgents: () => request<{ items: Agent[] }>('/agents'),
  getSessions: (agentId?: string) => {
    const query = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
    return request<{ items: Session[] }>(`/sessions${query}`)
  },
  getMessages: (sessionId: string, agentId: string, before?: string, limit = 50) => {
    const query = new URLSearchParams({ agent_id: agentId, limit: String(limit) })
    if (before) query.set('before', before)
    return request<{ items: import('../domain/types').Message[]; next_cursor: string | null }>(
      `${sessionPath(sessionId)}/messages?${query.toString()}`,
    )
  },
  getCommands: (sessionId: string, agentId: string) =>
    request<{ items: Command[] }>(
      `${sessionPath(sessionId)}/commands?agent_id=${encodeURIComponent(agentId)}`,
    ),
  uploadAttachment: (file: File) => {
    const form = new FormData()
    form.append('file', file, file.name)
    return request<Attachment>('/attachments', { method: 'POST', body: form })
  },
  createCommand: (payload: CommandPayload) => jsonRequest<Command>('/commands', payload),
  getRuntime: () => request<RuntimePayload>('/runtime'),
  launchRuntime: (kind: string, workspace: string) =>
    jsonRequest<{ agent_id: string; status: string }>('/runtime/launch', { kind, workspace }),
}

export type ApiClient = typeof api
