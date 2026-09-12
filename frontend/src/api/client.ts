import type {
  Agent,
  Attachment,
  AuthSession,
  BootstrapPayload,
  Command,
  ConnectionHistoryEntry,
  CommandPayload,
  ConnectionsPayload,
  RuntimePayload,
  Session,
  SshConnection,
  SshConnectionSettings,
  Task,
} from '../domain/types'

const API_PREFIX = '/api/v1'
import type { PreferencePatch, WorkspacePreferences } from '../hooks/useWorkspacePreferences'

export interface SessionModelBinding { model: string; provider: string | null; deferred?: boolean; branch?: string; effort?: string | null }
export interface CodexConnectionStatus { kind: 'codex'; state: 'disconnected' | 'connecting' | 'connected' | 'error' | 'authentication_required'; available: boolean; agent_id: string; session_count: number; auth_required: boolean; detail: string; daemon_mode: boolean }

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
  let localToken: string | null = null
  try {
    if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
      localToken = localStorage.getItem('astrorder:token')
    }
  } catch {}
  if (localToken && !headers.has('Authorization')) {
    headers.set('Authorization', `Bearer ${localToken}`)
  }
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
  getPreferences: () => request<WorkspacePreferences>('/preferences'),
  importPreferences: (values: PreferencePatch) => jsonRequest<WorkspacePreferences>('/preferences/import', values),
  updatePreferences: (values: PreferencePatch) => jsonRequest<WorkspacePreferences>('/preferences', values, 'PATCH'),
  getObservations: (agentId: string, sessionId?: string) => request<{ status: { installed?: boolean; trusted?: boolean; needs_review?: boolean; last_event_at?: number }; items: { id: string; event: string; label: string; observed_at: number; tool_name?: string }[] }>(`/agents/${encodeURIComponent(agentId)}/observations${sessionId ? '?session_id='+encodeURIComponent(sessionId) : ''}`),
  installObserver: (agentId: string) => jsonRequest(`/agents/${encodeURIComponent(agentId)}/observer`, {}),
  getAuthSession: () => request<AuthSession>('/auth/session'),
  login: (token: string) => jsonRequest<AuthSession>('/auth/session', { token }),
  logout: () => request<void>('/auth/session', { method: 'DELETE' }),
  getBootstrap: () => request<BootstrapPayload>('/bootstrap'),
  getAgents: () => request<{ items: Agent[] }>('/agents'),
  getSessionModel: (sessionId: string, agentId: string) => request<SessionModelBinding>(`${sessionPath(sessionId)}/model?agent_id=${encodeURIComponent(agentId)}`),
  getOpenSessions: () => request<{ known_agent_ids: string[]; items: { agent_id: string; id: string }[]; live?: { agent_id: string; id: string }[] }>('/open-sessions'),
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
  getTasks: (sessionId: string, agentId: string) =>
    request<{ items: Task[] }>(
      `${sessionPath(sessionId)}/tasks?agent_id=${encodeURIComponent(agentId)}`,
    ),
  uploadAttachment: (file: File) => {
    const form = new FormData()
    form.append('file', file, file.name)
    return request<Attachment>('/attachments', { method: 'POST', body: form })
  },
  createCommand: (payload: CommandPayload) => jsonRequest<Command>('/commands', payload),
  createSession: (payload: { agent_id: string; workspace?: string | null; title?: string | null; project_id?: string | null; project_name?: string | null; parent_session_id?: string | null; ephemeral?: boolean }) =>
    jsonRequest<Session>('/sessions', payload),
  updateSession: (sessionId: string, payload: { agent_id: string; title?: string | null; workspace?: string | null; status?: string | null }) =>
    jsonRequest<Session>(sessionPath(sessionId), payload, 'PATCH'),
  deleteSession: (sessionId: string, agentId: string) =>
    request<{ ok: boolean; id: string }>(`${sessionPath(sessionId)}?agent_id=${encodeURIComponent(agentId)}`, {
      method: 'DELETE',
    }),
  deleteProject: (payload: {
    project_key?: string
    project_id?: string
    source_id?: string
    workspace?: string | null
    session_keys?: Array<{ agent_id: string; id: string }>
    delete_sessions?: boolean
  }) =>
    jsonRequest<{ ok: boolean; deleted_sessions: Array<{ agent_id: string; id: string }> }>(
      '/projects/delete',
      payload,
    ),
  getSessionModels: (sessionId: string, agentId: string) => request<{ items: { provider: string; model: string; label: string }[] }>(`${sessionPath(sessionId)}/models?agent_id=${encodeURIComponent(agentId)}`),
  setSessionModel: (sessionId: string, agentId: string, provider: string, model: string) => jsonRequest<{ provider: string; model: string; deferred?: boolean }>(`${sessionPath(sessionId)}/model`, { agent_id: agentId, provider, model }),
  setSessionReasoning: (sessionId: string, agentId: string, effort: string) => jsonRequest<{ effort: string }>(`${sessionPath(sessionId)}/reasoning`, { agent_id: agentId, effort }),
  getSessionApprovalMode: (sessionId: string, agentId: string) => request<{ mode: import('../domain/types').ApprovalMode }>(`${sessionPath(sessionId)}/approval-mode?agent_id=${encodeURIComponent(agentId)}`),
  setSessionApprovalMode: (sessionId: string, agentId: string, mode: import('../domain/types').ApprovalMode) => jsonRequest<{ mode: import('../domain/types').ApprovalMode }>(`${sessionPath(sessionId)}/approval-mode`, { agent_id: agentId, mode }),
  getRuntime: () => request<RuntimePayload>('/runtime'),
  launchRuntime: (kind: string, workspace: string) =>
    jsonRequest<{ agent_id: string; status: string }>('/runtime/launch', { kind, workspace }),
  getUserActivity: () => request<{ items: Array<{ agent_id: string; id: string; last_user_at: string }>; live?: Array<{ agent_id: string; id: string }> }>('/user-activity'),
  getConnections: () => request<ConnectionsPayload>('/connections'),
  getEnvironments: () => request<{ items: Array<{ id: string; name: string; method: 'local' | 'ssh'; discovered: boolean; os?: string; agents: Array<{ kind: 'hermes' | 'codex'; available: boolean; state: string; detail: string; executable?: string; daemon_mode?: boolean }> }> }>('/environments'),
  discoverEnvironment: (id: string) => request(`/environments/${encodeURIComponent(id)}/discover`, { method: 'POST' }),
  changeEnvironmentAgent: (id: string, kind: string, connect: boolean) => request(`/environments/${encodeURIComponent(id)}/agents/${kind}/${connect ? 'connect' : 'disconnect'}`, { method: 'POST' }),
  getCodexConnection: () => request<CodexConnectionStatus>('/connections/codex'),
  connectCodex: () => request<CodexConnectionStatus>('/connections/codex/connect', { method: 'POST' }),
  disconnectCodex: () => request<CodexConnectionStatus>('/connections/codex/disconnect', { method: 'POST' }),
  getConnectionHistory: (connectionId: string, before?: string) =>
    request<{ items: ConnectionHistoryEntry[]; next_cursor: string | null }>(
      `/connections/${encodeURIComponent(connectionId)}/history?limit=50${before ? `&before=${encodeURIComponent(before)}` : ''}`,
    ),
  connectLocalHermes: () => request<ConnectionsPayload>('/connections/local/connect', { method: 'POST' }),
  disconnectLocalHermes: () => request<ConnectionsPayload>('/connections/local/disconnect', { method: 'POST' }),
  saveSshConnection: (settings: SshConnectionSettings) =>
    jsonRequest<SshConnection>('/connections/ssh', settings, 'PUT'),
  updateSshConnection: (connectionId: string, settings: SshConnectionSettings) =>
    jsonRequest<SshConnection>(`/connections/ssh/${encodeURIComponent(connectionId)}`, settings, 'PUT'),
  testSshConnection: (connectionId?: string) =>
    request<ConnectionsPayload>(
      connectionId ? `/connections/ssh/${encodeURIComponent(connectionId)}/test` : '/connections/ssh/test',
      { method: 'POST' },
    ),
  connectSshConnection: (connectionId?: string) =>
    request<ConnectionsPayload>(
      connectionId ? `/connections/ssh/${encodeURIComponent(connectionId)}/connect` : '/connections/ssh/connect',
      { method: 'POST' },
    ),
  disconnectSshConnection: (connectionId?: string) =>
    request<ConnectionsPayload>(
      connectionId ? `/connections/ssh/${encodeURIComponent(connectionId)}/disconnect` : '/connections/ssh/disconnect',
      { method: 'POST' },
    ),
  deleteSshConnection: (connectionId: string) =>
    request<ConnectionsPayload>(`/connections/ssh/${encodeURIComponent(connectionId)}`, { method: 'DELETE' }),
  searchFiles: (params: { q?: string; sessionId?: string; connectionId?: string; limit?: number }) => {
    const query = new URLSearchParams()
    if (params.q) query.set('q', params.q)
    if (params.sessionId) query.set('session_id', params.sessionId)
    if (params.connectionId) query.set('connection_id', params.connectionId)
    if (params.limit) query.set('limit', String(params.limit))
    return request<{ root: string; items: Array<{ path: string; name: string; size: number }> }>(`/files/search?${query.toString()}`)
  },
}

export type ApiClient = typeof api
