import type {
  Agent,
  AgentKind,
  AgentCommand,
  AgentMention,
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
export interface OcxUsageBreakdown {
  provider: string
  model?: string
  requests: number
  attemptCount: number
  measuredRequests: number
  reportedRequests: number
  estimatedRequests: number
  totalTokens: number
  inputTokens: number
  outputTokens: number
  cachedInputTokens: number
  cacheReadInputTokens: number
  cacheCreationInputTokens: number
  cacheHitRate: number | null
  cacheObservedInputTokens: number
  priceCoverageRatio: number
  pricedRequests: number
  unpricedRequests: number
  shareRatio: number
  estimatedCostUsd: number
}
export interface OcxUsageResponse {
  range: string
  surface: string
  since: number
  generatedAt: string
  summary: Record<string, number>
  days: Array<{ date: string; requests: number; measuredRequests: number; reportedRequests: number; totalTokens: number; estimatedCostUsd: number; models?: Array<{ model: string; provider: string; requests: number; totalTokens: number; inputTokens?: number; outputTokens?: number }> }>
  models: OcxUsageBreakdown[]
  providers: OcxUsageBreakdown[]
  accounts: Array<{ accountLogLabel: string; ambiguous: boolean; requests: number; attemptCount: number; measuredAttempts: number; reportedAttempts: number; estimatedAttempts: number; unmeteredAttempts: number; inputTokens: number; outputTokens: number; cacheReadInputTokens: number; cacheCreationInputTokens: number; reasoningOutputTokens: number; totalTokens: number; usageCoverageRatio: number; estimatedCostUsd: number; pricedAttempts: number; unpricedAttempts: number; priceCoverageRatio: number }>
  historyTruncated: boolean
  truncatedPrefixBytes: number
  entriesTruncated: boolean
  entriesDropped: number
  snapshotWindowStart?: number
  snapshotWindowEnd?: number
}
export interface OcxQuotaWindow { label: string; percent: number; resetAt?: number }
export interface OcxModelQuotaReport {
  provider: string
  label: string
  quota?: { fiveHourPercent?: number; fiveHourResetAt?: number; weeklyPercent?: number; weeklyResetAt?: number; monthlyPercent?: number; monthlyResetAt?: number; customWindows?: OcxQuotaWindow[] }
}
export interface OcxModelQuotaAccount { id: string; email?: string; logLabel?: string; plan?: string; paused?: boolean; quota?: { shortPercent?: number; shortResetAt?: number; weeklyPercent?: number; weeklyResetAt?: number } | null }
export interface OcxModelQuotaResponse { model: string; providers: string[]; reports: OcxModelQuotaReport[]; accounts: OcxModelQuotaAccount[] }

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

export interface BrowserTab {
  id: string
  title: string
  url: string
  active: boolean
}

export interface BrowserLog {
  type: 'error' | 'warn' | 'log' | 'info' | 'network_error'
  text: string
  time: number
}

export interface BrowserSnapshot {
  url: string
  title: string
  screenshot: string
  mime_type: string
  revision: number
  captured_at: number
  tabs?: BrowserTab[]
  active_target_id?: string
  error_count?: number
}

export const api = {
  getBrowserScreenshot: (agentId: string, sessionId: string, refresh = false, targetId?: string) => request<BrowserSnapshot>(
    `/browser/screenshot?agent_id=${encodeURIComponent(agentId)}&session_id=${encodeURIComponent(sessionId)}&refresh=${refresh}${targetId ? '&target_id=' + encodeURIComponent(targetId) : ''}`
  ),
  navigateBrowser: (agentId: string, sessionId: string, url: string) => jsonRequest<BrowserSnapshot>('/browser/navigate', { agent_id: agentId, session_id: sessionId, url }),
  selectBrowserTab: (agentId: string, sessionId: string, targetId: string) => jsonRequest<BrowserSnapshot>('/browser/tabs/select', { agent_id: agentId, session_id: sessionId, target_id: targetId }),
  newBrowserTab: (agentId: string, sessionId: string, url?: string) => jsonRequest<BrowserSnapshot>('/browser/tabs/new', { agent_id: agentId, session_id: sessionId, url }),
  closeBrowserTab: (agentId: string, sessionId: string, targetId: string) => jsonRequest<BrowserSnapshot>('/browser/tabs/close', { agent_id: agentId, session_id: sessionId, target_id: targetId }),
  interactBrowser: (
    agentId: string,
    sessionId: string,
    action: 'click' | 'wheel' | 'text' | 'back' | 'forward',
    params?: { x?: number; y?: number; ratio_x?: number; ratio_y?: number; delta_y?: number; text?: string; targetId?: string }
  ) => jsonRequest<BrowserSnapshot>('/browser/interact', {
    agent_id: agentId,
    session_id: sessionId,
    action,
    target_id: params?.targetId,
    x: params?.x,
    y: params?.y,
    ratio_x: params?.ratio_x,
    ratio_y: params?.ratio_y,
    delta_y: params?.delta_y,
    text: params?.text,
  }),
  getBrowserPageContent: (agentId: string, sessionId: string, targetId?: string) => request<{
    title: string
    url: string
    text: string
    session_key: string
    target_id: string
  }>(`/browser/page-content?agent_id=${encodeURIComponent(agentId)}&session_id=${encodeURIComponent(sessionId)}${targetId ? '&target_id=' + encodeURIComponent(targetId) : ''}`),
  getBrowserDiagnostics: (agentId: string, sessionId: string, targetId?: string) => request<{
    session_key: string
    target_id?: string
    logs: BrowserLog[]
  }>(`/browser/diagnostics?agent_id=${encodeURIComponent(agentId)}&session_id=${encodeURIComponent(sessionId)}${targetId ? '&target_id=' + encodeURIComponent(targetId) : ''}`),
  getPreferences: () => request<WorkspacePreferences>('/preferences'),
  importPreferences: (values: PreferencePatch) => jsonRequest<WorkspacePreferences>('/preferences/import', values),
  updatePreferences: (values: PreferencePatch) => jsonRequest<WorkspacePreferences>('/preferences', values, 'PATCH'),
  getObservations: (agentId: string, sessionId?: string) => request<{ status: { installed?: boolean; trusted?: boolean; needs_review?: boolean; last_event_at?: number }; items: { id: string; event: string; label: string; observed_at: number; tool_name?: string }[] }>(`/agents/${encodeURIComponent(agentId)}/observations${sessionId ? '?session_id='+encodeURIComponent(sessionId) : ''}`),
  installObserver: (agentId: string) => jsonRequest(`/agents/${encodeURIComponent(agentId)}/observer`, {}),
  getAuthSession: () => request<AuthSession>('/auth/session'),
  login: (token: string) => jsonRequest<AuthSession>('/auth/session', { token }),
  logout: () => request<void>('/auth/session', { method: 'DELETE' }),
  getBootstrap: () => request<BootstrapPayload>('/bootstrap'),
  getPresence: () => request<{ items: Array<{ agent_id: string; id: string; last_user_at: string }>; open?: Array<{ agent_id: string; id: string }>; live?: Array<{ agent_id: string; id: string }> }>('/presence'),
  getAgents: () => request<{ items: Agent[] }>('/agents'),
  getSessionModel: (sessionId: string, agentId: string) => request<SessionModelBinding>(`${sessionPath(sessionId)}/model?agent_id=${encodeURIComponent(agentId)}`),
  getOpenSessions: () => request<{ known_agent_ids: string[]; items: { agent_id: string; id: string }[]; live?: { agent_id: string; id: string }[] }>('/open-sessions'),
  getSessions: (agentId?: string) => {
    const query = agentId ? `?agent_id=${encodeURIComponent(agentId)}` : ''
    return request<{ items: Session[] }>(`/sessions${query}`)
  },
  syncSession: (sessionId: string, agentId: string) =>
    request<Session>(`${sessionPath(sessionId)}/sync?agent_id=${encodeURIComponent(agentId)}`, { method: 'POST' }),
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
    const desktop = (window as unknown as { astrorderDesktop?: { getPathForFile?: (file: File) => string } }).astrorderDesktop
    const sourcePath = desktop?.getPathForFile?.(file)
    if (sourcePath) form.append('source_path', sourcePath)
    return request<Attachment>('/attachments', { method: 'POST', body: form })
  },
  createCommand: (payload: CommandPayload) => jsonRequest<Command>('/commands', payload),
  editQueuedCommand: (commandId: string, agentId: string, sessionId: string, text: string) =>
    jsonRequest<Command>(`/commands/${encodeURIComponent(commandId)}`, { agent_id: agentId, session_id: sessionId, text }, 'PATCH'),
  sendQueuedCommand: (commandId: string, agentId: string, sessionId: string) =>
    jsonRequest<Command>(`/commands/${encodeURIComponent(commandId)}/send`, { agent_id: agentId, session_id: sessionId }),
  deleteQueuedCommand: (commandId: string, agentId: string, sessionId: string) => {
    const query = new URLSearchParams({ agent_id: agentId, session_id: sessionId })
    return request<Command>(`/commands/${encodeURIComponent(commandId)}?${query.toString()}`, { method: 'DELETE' })
  },
  upgradeAgentStream: async (
    agentId: string,
    onChunk: (chunk: string) => void,
    onInit?: (cmd: string) => void,
    signal?: AbortSignal,
  ): Promise<{ ok: boolean; exit_code: number }> => {
    let localToken: string | null = null
    try {
      if (typeof localStorage !== 'undefined' && typeof localStorage.getItem === 'function') {
        localToken = localStorage.getItem('astrorder:token')
      }
    } catch {}
    const res = await fetch(`${API_PREFIX}/agents/${encodeURIComponent(agentId)}/upgrade`, {
      method: 'POST',
      headers: {
        ...(localToken ? { 'Authorization': `Bearer ${localToken}` } : {}),
      },
      signal,
    })
    if (!res.ok) {
      throw new Error(`升级请求失败: ${res.status} ${res.statusText}`)
    }
    const reader = res.body?.getReader()
    if (!reader) throw new Error('流式读取不可用')
    const decoder = new TextDecoder()
    let buffer = ''
    let isOk = false
    let exitCode = 0

    while (true) {
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const lines = buffer.split('\n')
      buffer = lines.pop() || ''
      for (const line of lines) {
        if (!line.trim()) continue
        try {
          const item = JSON.parse(line)
          if (item.type === 'init' && item.command) {
            onInit?.(item.command)
          } else if (item.type === 'chunk' && item.data) {
            onChunk(item.data)
          } else if (item.type === 'done') {
            isOk = Boolean(item.ok)
            exitCode = item.exit_code ?? 0
          } else if (item.type === 'error') {
            throw new Error(item.error || '升级遇到错误')
          }
        } catch {
          onChunk(line + '\n')
        }
      }
    }
    return { ok: isOk, exit_code: exitCode }
  },
  createSession: (payload: { agent_id: string; workspace?: string | null; title?: string | null; project_id?: string | null; project_name?: string | null; parent_session_id?: string | null; ephemeral?: boolean; blackboard_scope?: 'session' | 'swarm' | 'group'; blackboard_scope_id?: string | null }) =>
    jsonRequest<Session>('/sessions', payload),
  forkSession: (sessionId: string, payload: { agent_id: string; title?: string | null; worktree?: boolean; branch_name?: string | null; worktree_path?: string | null; target_message_id?: string | null; turn_index?: number | null }) =>
    jsonRequest<Session>(`${sessionPath(sessionId)}/fork`, payload),
  handoffSession: (sessionId: string, sourceAgentId: string, targetAgentId: string, operationId: string, selection: { provider: string; model: string; effort: string }) =>
    jsonRequest<Session>(`${sessionPath(sessionId)}/handoff`, { operation_id: operationId, source_agent_id: sourceAgentId, target_agent_id: targetAgentId, ...selection }),
  cancelHandoff: async (operationId: string) => {
    const result = await jsonRequest<{ cancelled: boolean }>(`/handoffs/${encodeURIComponent(operationId)}/cancel`, {})
    if (!result.cancelled) throw new Error('转交任务已结束，无法取消')
  },
  updateSession: (sessionId: string, payload: { agent_id: string; title?: string | null; workspace?: string | null; status?: string | null }) =>
    jsonRequest<Session>(sessionPath(sessionId), payload, 'PATCH'),
  deleteSession: (sessionId: string, agentId: string) =>
    request<{ ok: boolean; id: string }>(`${sessionPath(sessionId)}?agent_id=${encodeURIComponent(agentId)}`, {
      method: 'DELETE',
    }),
  getBlackboard: (namespace = 'global') =>
    request<{ namespace: string; items: Record<string, unknown>; count: number }>(`/blackboard?namespace=${encodeURIComponent(namespace)}`),
  setBlackboard: (key: string, value: unknown, namespace = 'global') =>
    jsonRequest<{ ok: boolean; key: string; value: unknown }>('/agent/invoke', {
      capability: 'blackboard.set',
      input: { namespace, key, value },
    }),
  deleteBlackboard: (key: string, namespace = 'global') =>
    jsonRequest<{ ok: boolean; key: string }>('/agent/invoke', {
      capability: 'blackboard.delete',
      input: { namespace, key },
    }),
  getTelemetry: (key?: string) =>
    jsonRequest<{ items?: Record<string, any>; report?: any; exists?: boolean }>('/agent/invoke', {
      capability: 'swarm.telemetry.get',
      input: { key },
    }),
  getAgentMcpStatus: (agentId: string) =>
    request<{ agent_id: string; enabled: boolean }>(`/agents/${encodeURIComponent(agentId)}/mcp`),
  toggleAgentMcpStatus: (agentId: string, enabled: boolean) =>
    jsonRequest<{ agent_id: string; enabled: boolean }>(`/agents/${encodeURIComponent(agentId)}/mcp`, {
      agent_id: agentId,
      enabled,
    }),
  listSos: () =>
    jsonRequest<{ items: any[]; count: number }>('/agent/invoke', {
      capability: 'swarm.sos.list',
      input: {},
    }),
  listBotGroups: () =>
    request<{ items: import('../domain/types').BotGroup[]; count: number }>('/bot-groups'),
  getBotGroup: (groupId: string) =>
    request<{ group: import('../domain/types').BotGroup }>(`/bot-groups/${encodeURIComponent(groupId)}`),
  createBotGroup: (payload: { name: string; description?: string | null; members: import('../domain/types').BotGroupMember[]; max_hops?: number }) =>
    jsonRequest<{ group: import('../domain/types').BotGroup; ok: boolean }>('/bot-groups', payload),
  updateBotGroup: (groupId: string, payload: Partial<{ name: string; description: string; members: import('../domain/types').BotGroupMember[]; max_hops: number }>) =>
    jsonRequest<{ group: import('../domain/types').BotGroup; ok: boolean }>(`/bot-groups/${encodeURIComponent(groupId)}`, payload, 'PATCH'),
  deleteBotGroup: (groupId: string) =>
    request<{ ok: boolean; id: string }>(`/bot-groups/${encodeURIComponent(groupId)}`, { method: 'DELETE' }),
  listGroupMessages: (groupId: string) =>
    request<{ items: import('../domain/types').GroupMessage[]; count: number }>(`/bot-groups/${encodeURIComponent(groupId)}/messages`),
  sendGroupMessage: (groupId: string, payload: { text: string; target_agent_id?: string | null; mentions?: string[] }) =>
    jsonRequest<{ ok: boolean; message: import('../domain/types').GroupMessage; target_agent_id: string | null; history_count: number }>(`/bot-groups/${encodeURIComponent(groupId)}/messages`, payload),
  stopGroup: (groupId: string) =>
    jsonRequest<{ ok: boolean; group: import('../domain/types').BotGroup }>(`/bot-groups/${encodeURIComponent(groupId)}/stop`, {}),
  batchDeleteSessions: (sessions: Array<{ agent_id: string; id: string }>) =>
    jsonRequest<{ ok: boolean; deleted: Array<{ agent_id: string; id: string }>; failed: Array<{ agent_id: string; id: string; error: string }> }>(
      '/sessions/batch-delete',
      { sessions },
    ),
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
  createProject: (payload: {
    workspace: string
    name?: string
    connection_id?: string | null
    agent_id?: string | null
  }) =>
    jsonRequest<{ project: import('../domain/types').Project }>(
      '/projects',
      payload,
    ),
  getSessionModels: (sessionId: string, agentId: string) => request<{ items: { provider: string; model: string; label: string }[] }>(`${sessionPath(sessionId)}/models?agent_id=${encodeURIComponent(agentId)}`),
  getAgentCommands: (sessionId: string, agentId: string) => request<{ items: AgentCommand[] }>(`${sessionPath(sessionId)}/agent-commands?agent_id=${encodeURIComponent(agentId)}`),
  getAgentMentions: (sessionId: string, agentId: string) => request<{ items: AgentMention[] }>(`${sessionPath(sessionId)}/agent-mentions?agent_id=${encodeURIComponent(agentId)}`),
  setSessionModel: (sessionId: string, agentId: string, provider: string, model: string) => jsonRequest<{ provider: string; model: string; deferred?: boolean }>(`${sessionPath(sessionId)}/model`, { agent_id: agentId, provider, model }),
  setSessionReasoning: (sessionId: string, agentId: string, effort: string) => jsonRequest<{ effort: string }>(`${sessionPath(sessionId)}/reasoning`, { agent_id: agentId, effort }),
  getSessionApprovalMode: (sessionId: string, agentId: string) => request<{ mode: import('../domain/types').ApprovalMode }>(`${sessionPath(sessionId)}/approval-mode?agent_id=${encodeURIComponent(agentId)}`),
  setSessionApprovalMode: (sessionId: string, agentId: string, mode: import('../domain/types').ApprovalMode) => jsonRequest<{ mode: import('../domain/types').ApprovalMode }>(`${sessionPath(sessionId)}/approval-mode`, { agent_id: agentId, mode }),
  getRuntime: () => request<RuntimePayload>('/runtime'),
  launchRuntime: (kind: string, workspace: string) =>
    jsonRequest<{ agent_id: string; status: string }>('/runtime/launch', { kind, workspace }),
  getUserActivity: () => request<{ items: Array<{ agent_id: string; id: string; last_user_at: string }>; live?: Array<{ agent_id: string; id: string }> }>('/user-activity'),
  getNetworkConfig: () =>
    request<{ public_url: string; allowed_origins: string[]; local_ip: string; port: number; token: string }>('/network/config'),
  updateNetworkConfig: (payload: { public_url?: string; allowed_origins?: string[] }) =>
    jsonRequest<{ public_url: string; allowed_origins: string[]; local_ip: string; port: number; token: string }>('/network/config', payload),
  getJevConfig: () =>
    request<{ configured: boolean; masked_key: string }>('/services/jev/config'),
  updateJevConfig: (payload: { api_key?: string | null }) =>
    jsonRequest<{ configured: boolean; masked_key: string }>('/services/jev/config', payload),
  getLlmConfig: () =>
    request<{ configured: boolean; masked_key: string; base_url: string; model: string; reasoning: string }>('/services/llm/config'),
  updateLlmConfig: (payload: { base_url: string; api_key?: string | null; model: string; reasoning: string }) =>
    jsonRequest<{ configured: boolean; masked_key: string; base_url: string; model: string; reasoning: string }>('/services/llm/config', payload),
  testLlmConnection: (payload: { base_url: string; api_key?: string | null; model: string; reasoning: string }) =>
    jsonRequest<{ ok: boolean; model: string; reply: string }>('/services/llm/test', payload),
  filterWithJev: (payload: { command: string; output: string }) =>
    jsonRequest<{ ok: boolean; filtered: string; nature: string; noise_pruned: boolean }>('/tools/jev-filter', payload),
  curateHandoffWithJev: (sessionId: string, agentId: string) =>
    jsonRequest<{ ok: boolean; summary: string; phase: string; blocker: string }>('/sessions/handoff-curate', { session_id: sessionId, agent_id: agentId }),
  testJevConnection: (payload: { api_key?: string | null }) =>
    jsonRequest<{ ok: boolean; details: any }>('/services/jev/test', payload),
  getConnections: () => request<ConnectionsPayload>('/connections'),
  getEnvironments: () => request<{ items: Array<{ id: string; name: string; method: 'local' | 'ssh'; discovered: boolean; os?: string; agents: Array<{ kind: AgentKind; available: boolean; state: string; detail: string; executable?: string; daemon_mode?: boolean }> }> }>('/environments'),
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
  getSessionUsage: (sessionId: string, agentId?: string) =>
    request<{
      ok: boolean
      session_id: string
      model: string
      context_window: number
      last_input_tokens: number
      used_percentage: number
      total_tokens: number
      input_tokens: number
      output_tokens: number
      cached_tokens: number
      reasoning_tokens: number
      cache_hit_rate: number
      speed?: number | null
    }>(`/sessions/${encodeURIComponent(sessionId)}/usage${agentId ? '?agent_id=' + encodeURIComponent(agentId) : ''}`),
  getAnalyticsConfig: () => request<{ gateway_type: 'opencodex'; management_url: string; inference_url: string; target_overrides: Record<string, string>; masked_key: string }>('/analytics/config'),
  updateAnalyticsConfig: (payload: { gateway_type: 'opencodex'; management_url: string; inference_url: string; target_overrides: Record<string, string>; api_key?: string }) => jsonRequest<{ gateway_type: 'opencodex'; management_url: string; inference_url: string; target_overrides: Record<string, string>; masked_key: string }>('/analytics/config', payload, 'PUT'),
  getModelSyncTargets: () => request<{ items: Array<{ id: string; kind: 'local' | 'wsl' | 'ssh'; name: string; state?: string; agents: string[] }> }>('/model-sync/targets'),
  previewModelSync: (targets: Array<{ target_id: string; agents: string[] }>) => jsonRequest<{
    catalog_fingerprint: string
    model_count: number
    targets: Array<{
      target_id: string
      agents: string[]
      reload_pending: boolean
      hermes: { dynamic: boolean; status: string }
      model_diff?: { added: string[]; removed: string[]; kept_count: number }
      changes: Array<{ file: string; changed: boolean; current_sha256: string; expected_sha256: string; diff: string }>
    }>
  }>('/model-sync/preview', { targets }),
  startModelSync: (targets: Array<{ target_id: string; agents: string[] }>) => jsonRequest<{ id: string; status: string }>('/model-sync/jobs', { targets }),
  getModelSyncJobs: () => request<{ items: Array<{ id: string; status: 'running' | 'success' | 'failed' | 'cancelled'; error?: string; targets: Array<{ target_id: string; status: string; changed: string[]; reload_pending: boolean }> }> }>('/model-sync/jobs'),
  cancelModelSync: (id: string) => request<{ cancelled: boolean }>(`/model-sync/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST' }),
  getAnalyticsUsage: (params: { range: 'all' | '30d' | '7d'; surface: 'all' | 'codex' | 'claude' | 'grok'; since?: number; until?: number }) => {
    const query = new URLSearchParams({ range: params.range, surface: params.surface })
    if (params.since !== undefined) query.set('since', String(params.since))
    if (params.until !== undefined) query.set('until', String(params.until))
    return request<OcxUsageResponse>(`/analytics/usage?${query.toString()}`)
  },
  getModelQuota: (model: string) => request<OcxModelQuotaResponse>(`/analytics/model-quota?model=${encodeURIComponent(model)}`),
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
