import { create } from 'zustand'
import { eventIsNew, isEphemeralSession, mergeMessagesById, scopeKey } from '../domain/semantics'
import { visibleHermesMessage } from './hermesMemoryContext'
import type {
  Agent,
  Approval,
  BootstrapPayload,
  BotGroup,
  Command,
  CommandState,
  ConnectionStatus,
  DraftState,
  EventEnvelope,
  EventNotification,
  Message,
  OutboxEntry,
  OutboxStatus,
  Project,
  Session,
  Task,
} from '../domain/types'

export interface AstrorderStore {
  agents: Record<string, Agent>
  disabledAgentConnections: ReadonlySet<string>
  projects: Record<string, Project>
  sessions: Record<string, Session>
  messages: Record<string, Record<string, Message>>
  messageRevision: number
  messageEventRevisions: Record<string, Record<string, number>>
  messageCatchUps: Record<string, Record<string, string>>
  messageStreams: Record<string, Record<string, true>>
  commands: Record<string, Command>
  tasks: Record<string, Task>
  approvals: Record<string, Approval>
  notifications: Record<string, EventNotification>
  botGroups: Record<string, BotGroup>
  botGroupRevision: number
  botGroupEventRevisions: Record<string, number>
  outbox: Record<string, OutboxEntry>
  drafts: Record<string, DraftState>
  liveActivityAt: Record<string, number>
  cursor: number
  seenEventIds: Record<string, true>
  connection: ConnectionStatus
  resyncRequired: boolean
  setConnection: (connection: ConnectionStatus) => void
  hydrateBootstrap: (payload: BootstrapPayload) => void
  mergeMessages: (agentId: string, sessionId: string, items: Message[], ahead?: Record<string, string>) => void
  mergeMessagesPrepend: (agentId: string, sessionId: string, items: Message[], ahead?: Record<string, string>) => void
  mergeCommands: (items: Command[]) => void
  mergeTasks: (items: Task[]) => void
  addNotification: (notification: EventNotification) => boolean
  hydrateBotGroups: (items: BotGroup[], revisionAtRequest: number) => void
  addOutbox: (command: Command, status?: OutboxStatus) => void
  updateOutboxAttachments: (
    agentId: string,
    sessionId: string,
    commandId: string,
    attachments: Command['attachments'],
  ) => void
    removeOutbox: (agentId: string, sessionId: string, commandId: string) => void
markOutboxError: (
    agentId: string,
    sessionId: string,
    commandId: string,
    error: string,
    status?: OutboxStatus,
  ) => void
  applyEvent: (event: EventEnvelope) => void
  applyMessageDeltas: (events: EventEnvelope[]) => void
  setDraft: (agentId: string, sessionId: string, draft: DraftState) => void
  setDraftText: (agentId: string, sessionId: string, text: string) => void
  updateSession: (agentId: string, sessionId: string, patch: Partial<Session>) => void
  commitOptimisticSend: (params: {
    command: Command
    optimisticMessage?: Message | null
    clearDraft?: boolean
    markRunning?: boolean
  }) => void
  resetRuntime: () => void
}

const emptyDraft = (): DraftState => ({ text: '', attachments: [], sessionRefs: [] })
const MAX_SEEN_EVENT_IDS = 1024
const MAX_CACHED_MESSAGE_SESSIONS = 12
const MAX_CACHED_TASKS = 800
const LIVE_ACTIVITY_REFRESH_MS = 5000
export const LIVE_ACTIVITY_MS = 15_000
export const MID_TURN_IDLE_GRACE_MS = 10_000
let activityExpiryTimer: ReturnType<typeof setTimeout> | undefined
let sessionAccessOrder: string[] = []

function touchSessionScope(key: string): void {
  const idx = sessionAccessOrder.indexOf(key)
  if (idx >= 0) {
    if (idx === sessionAccessOrder.length - 1) return
    sessionAccessOrder.splice(idx, 1)
  }
  sessionAccessOrder.push(key)
}

function isTerminalTaskStatus(status: Task['status']): boolean {
  return status === 'completed' || status === 'failed' || status === 'cancelled'
}

function isProtectedSessionScope(state: AstrorderStore, key: string, activeKey: string): boolean {
  if (key === activeKey) return true
  const session = state.sessions[key]
  if (session && (session.status === 'running' || session.status === 'waiting_approval')) return true
  if (key in state.liveActivityAt) return true
  if (state.messageStreams[key] && Object.keys(state.messageStreams[key]).length > 0) return true
  const prefix = `${key}::`
  for (const outboxKey of Object.keys(state.outbox)) {
    if (outboxKey.startsWith(prefix)) return true
  }
  return false
}

function pruneInactiveMessageScopes(
  state: AstrorderStore,
  activeKey: string,
  messages: Record<string, Record<string, Message>>,
  messageEventRevisions: Record<string, Record<string, number>>,
  messageCatchUps: Record<string, Record<string, string>>,
  messageStreams: Record<string, Record<string, true>>,
): Pick<AstrorderStore, 'messages' | 'messageEventRevisions' | 'messageCatchUps' | 'messageStreams'> {
  touchSessionScope(activeKey)
  const totalScopes = Object.keys(messages).length
  if (totalScopes <= MAX_CACHED_MESSAGE_SESSIONS) {
    return { messages, messageEventRevisions, messageCatchUps, messageStreams }
  }
  let overflow = totalScopes - MAX_CACHED_MESSAGE_SESSIONS
  const nextMessages = { ...messages }
  let nextRevisions = messageEventRevisions
  let nextCatchUps = messageCatchUps
  let nextStreams = messageStreams
  const remainingOrder: string[] = []

  for (const key of sessionAccessOrder) {
    if (!(key in nextMessages)) continue
    if (overflow > 0 && !isProtectedSessionScope(state, key, activeKey)) {
      delete nextMessages[key]
      if (key in nextRevisions) {
        if (nextRevisions === messageEventRevisions) nextRevisions = { ...messageEventRevisions }
        delete nextRevisions[key]
      }
      if (key in nextCatchUps) {
        if (nextCatchUps === messageCatchUps) nextCatchUps = { ...messageCatchUps }
        delete nextCatchUps[key]
      }
      if (key in nextStreams) {
        if (nextStreams === messageStreams) nextStreams = { ...messageStreams }
        delete nextStreams[key]
      }
      overflow -= 1
      continue
    }
    remainingOrder.push(key)
  }
  sessionAccessOrder = remainingOrder
  return {
    messages: nextMessages,
    messageEventRevisions: nextRevisions,
    messageCatchUps: nextCatchUps,
    messageStreams: nextStreams,
  }
}

function pruneTaskMap(tasks: Record<string, Task>, protectedScopes: Set<string>): Record<string, Task> {
  const entries = Object.entries(tasks)
  if (entries.length <= MAX_CACHED_TASKS) return tasks
  let overflow = entries.length - MAX_CACHED_TASKS
  const evictable: Array<[string, Task]> = []
  for (const entry of entries) {
    const [, task] = entry
    if (!isTerminalTaskStatus(task.status)) continue
    if (protectedScopes.has(scopeKey(task.agent_id, task.session_id))) continue
    evictable.push(entry)
  }
  if (evictable.length === 0) return tasks
  evictable.sort((a, b) => (a[1].updated_at || '').localeCompare(b[1].updated_at || ''))
  const next = { ...tasks }
  for (let i = 0; i < evictable.length && overflow > 0; i += 1) {
    delete next[evictable[i][0]]
    overflow -= 1
  }
  return next
}

function scheduleActivityExpiry(activity: Record<string, number>): void {
  clearTimeout(activityExpiryTimer)
  activityExpiryTimer = undefined
  const timestamps = Object.values(activity)
  if (!timestamps.length) return
  const delay = Math.max(0, Math.min(...timestamps) + LIVE_ACTIVITY_MS - Date.now())
  activityExpiryTimer = setTimeout(() => {
    const current = useAstrorderStore.getState().liveActivityAt
    const remaining = Object.fromEntries(Object.entries(current).filter(([, at]) => Date.now() - at < LIVE_ACTIVITY_MS))
    useAstrorderStore.setState({ liveActivityAt: remaining })
    scheduleActivityExpiry(remaining)
  }, delay)
}

function clearLiveActivity(current: Record<string, number>, key: string): Record<string, number> {
  if (!(key in current)) return current
  const next = { ...current }
  delete next[key]
  scheduleActivityExpiry(next)
  return next
}

function isSessionMidTurnActivity(messages: Record<string, Message> | undefined): boolean {
  if (!messages) return false
  let latest: Message | null = null
  let latestTs = -Infinity
  for (const msg of Object.values(messages)) {
    if (!msg) continue
    const hasBody = Boolean(msg.text?.trim() || msg.tool || msg.attachments?.length)
    if (!hasBody) continue
    const ts = Date.parse(msg.created_at) || 0
    if (!latest || ts >= latestTs) {
      latest = msg
      latestTs = ts
    }
  }
  if (!latest) return false
  return latest.role === 'tool' || latest.kind === 'tool' || latest.kind === 'thinking'
}

function settleLiveActivityOnIdle(
  state: AstrorderStore,
  key: string,
  prev: Session | undefined,
): Record<string, number> {
  const wasActive = prev?.status === 'running' || key in state.liveActivityAt
  if (!wasActive || !isSessionMidTurnActivity(state.messages[key])) {
    return clearLiveActivity(state.liveActivityAt, key)
  }
  // Duplicate idle upserts while already counting down must not extend the grace timer.
  if (prev?.status === 'idle' && key in state.liveActivityAt) {
    return state.liveActivityAt
  }
  const graceTimestamp = Date.now() - (LIVE_ACTIVITY_MS - MID_TURN_IDLE_GRACE_MS)
  const next = { ...state.liveActivityAt, [key]: graceTimestamp }
  scheduleActivityExpiry(next)
  return next
}

function rememberEvent(seen: Record<string, true>, key: string): Record<string, true> {
  const keys = Object.keys(seen)
  if (keys.length < MAX_SEEN_EVENT_IDS) return { ...seen, [key]: true }
  return Object.fromEntries([...keys.slice(-MAX_SEEN_EVENT_IDS / 2), key].map((id) => [id, true]))
}

function touchLiveActivity(current: Record<string, number>, key: string): Record<string, number> {
  const now = Date.now()
  if (key in current && now - current[key] < LIVE_ACTIVITY_REFRESH_MS) return current
  const next = { ...current, [key]: now }
  scheduleActivityExpiry(next)
  return next
}

function commandKey(command: Command): string {
  return scopeKey(command.agent_id, command.session_id) + `::${command.id}`
}

function taskKey(task: Task): string {
  return scopeKey(task.agent_id, task.session_id) + `::${task.id}`
}

function messageMap(items: Message[]): Record<string, Message> {
  return Object.fromEntries(items.map((item) => [item.id, visibleHermesMessage(item)]))
}

function historyCatchUps(state: AstrorderStore, key: string, items: Message[], ahead: Record<string, string> = {}) {
  const pending = { ...state.messageCatchUps[key] }
  const current = state.messages[key] || {}
  for (const item of items) {
    const previous = current[item.id]
    if (ahead[item.id] !== undefined) {
      pending[item.id] = (pending[item.id] || '') + ahead[item.id]
    } else if (pending[item.id] && previous) {
      if (item.text.startsWith(previous.text)) pending[item.id] += item.text.slice(previous.text.length)
      else delete pending[item.id]
    }
  }
  if (Object.keys(pending).length) return { ...state.messageCatchUps, [key]: pending }
  const result = { ...state.messageCatchUps }
  delete result[key]
  return result
}

function liveMessageUpdate(state: AstrorderStore, event: EventEnvelope): Partial<AstrorderStore> | null {
  let message = event.data as unknown as Message
  if (!message.id || !message.session_id || !message.agent_id) return null
  const key = scopeKey(message.agent_id, message.session_id)
  const previous = state.messages[key]?.[message.id]
  const originalPending = state.messageCatchUps[key]?.[message.id]
  let pending = originalPending
  if (previous && event.type === 'message.upsert' && !message.text && !state.messageEventRevisions[key]?.[message.id]
    && message.role === 'assistant' && previous.role === 'assistant' && previous.text) {
    pending = previous.text
  }
  let messageCatchUps = state.messageCatchUps
  let consumedCatchUp = false
  if (previous && event.type === 'message.delta') {
    let text = previous.text + message.text
    if (pending) {
      if (pending.startsWith(message.text)) {
        text = previous.text
        pending = pending.slice(message.text.length)
        consumedCatchUp = true
      } else if (message.text.startsWith(pending)) {
        text = previous.text + message.text.slice(pending.length)
        pending = ''
      } else {
        text = previous.text.slice(0, -pending.length) + message.text
        pending = ''
      }
    }
    message = { ...previous, ...message, text, created_at: previous.created_at, command_id: message.command_id || previous.command_id }
  } else if (previous && pending && event.type === 'message.upsert') {
    if (!message.text) {
      message = { ...message, text: previous.text }
      consumedCatchUp = true
    } else {
      pending = ''
    }
  }
  if (originalPending !== pending) {
    const scope = { ...state.messageCatchUps[key] }
    if (pending) scope[message.id] = pending
    else delete scope[message.id]
    messageCatchUps = { ...state.messageCatchUps }
    if (Object.keys(scope).length) messageCatchUps[key] = scope
    else delete messageCatchUps[key]
  }
  if (previous && message.timestamp_source !== 'native') {
    message = { ...message, created_at: previous.created_at, timestamp_source: previous.timestamp_source, completed_at: previous.completed_at, turn_id: message.turn_id || previous.turn_id }
  }
  const current = Object.values(state.messages[key] || {})
  const merged = mergeMessagesById(current, [message])
  const streams = { ...state.messageStreams[key] }
  if (event.type === 'message.delta' || (message.role === 'assistant' && (!message.text || pending))) streams[message.id] = true
  else delete streams[message.id]
  const messageStreams = { ...state.messageStreams }
  if (Object.keys(streams).length) messageStreams[key] = streams
  else delete messageStreams[key]
  return {
    messages: { ...state.messages, [key]: messageMap(merged) },
    messageRevision: state.messageRevision + 1,
    messageEventRevisions: { ...state.messageEventRevisions, [key]: { ...state.messageEventRevisions[key], [message.id]: state.messageRevision + 1 } },
    messageCatchUps,
    messageStreams,
    liveActivityAt: message.tool?.background === true || consumedCatchUp ? state.liveActivityAt : touchLiveActivity(state.liveActivityAt, key),
  }
}

function mergeCommandIntoState(
  state: AstrorderStore,
  incoming: Command,
): Pick<AstrorderStore, 'commands' | 'outbox'> {
  const key = commandKey(incoming)
  const current = state.commands[key]
  const terminal = (value: Command['state']) => ['completed', 'failed', 'cancelled'].includes(value)
  if (current && terminal(current.state) && !terminal(incoming.state)) return { commands: state.commands, outbox: state.outbox }
  const command = current ? { ...current, ...incoming } : incoming
  const commands = { ...state.commands, [key]: command }
  const outboxKey = commandKey(incoming)
  const existingOutbox = state.outbox[outboxKey]
  if (!existingOutbox) {
    return {
      commands,
      outbox: {
        ...state.outbox,
        [outboxKey]: { command, status: command.state, error: command.error },
      },
    }
  }
  if (
    existingOutbox.command.agent_id !== command.agent_id ||
    existingOutbox.command.session_id !== command.session_id
  ) {
    return { commands, outbox: state.outbox }
  }
  return {
    commands,
    outbox: {
      ...state.outbox,
      [outboxKey]: {
        ...existingOutbox,
        command: { ...existingOutbox.command, ...command },
        status: command.state,
        error: command.error,
      },
    },
  }
}

function normalizeApproval(event: EventEnvelope): Approval | null {
  const data = event.data
  const id = typeof data.id === 'string' ? data.id : ''
  if (!id) return null
  const state = typeof data.state === 'string' ? data.state : typeof data.status === 'string' ? data.status : 'pending'
  const title = typeof data.title === 'string' ? data.title : '待确认操作'
  const detail =
    typeof data.detail === 'string'
      ? data.detail
      : typeof data.reason === 'string'
        ? data.reason
        : typeof data.message === 'string'
          ? data.message
          : ''
  return {
    id,
    agent_id: typeof data.agent_id === 'string' ? data.agent_id : event.agent_id,
    session_id: typeof data.session_id === 'string' ? data.session_id : event.session_id,
    title,
    detail,
    state,
    target_id: typeof data.target_id === 'string' ? data.target_id : null,
    data,
  }
}

export const useAstrorderStore = create<AstrorderStore>((set) => ({
  agents: {},
  disabledAgentConnections: new Set(),
  projects: {},
  sessions: {},
  messages: {},
  messageRevision: 0,
  messageEventRevisions: {},
  messageCatchUps: {},
  messageStreams: {},
  commands: {},
  tasks: {},
  approvals: {},
  notifications: {},
  botGroups: {},
  botGroupRevision: 0,
  botGroupEventRevisions: {},
  outbox: {},
  drafts: {},
  liveActivityAt: {},
  cursor: 0,
  seenEventIds: {},
  connection: 'disconnected',
  resyncRequired: false,

  setConnection: (connection) => set({ connection }),

  hydrateBootstrap: (payload) =>
    set((state) => {
      const agents: Record<string, Agent> = {}
      for (const agent of payload.agents) agents[agent.id] = agent
      const projects: Record<string, Project> = {}
      for (const project of payload.projects || []) projects[project.id] = project
      const sessions: Record<string, Session> = {}
      for (const session of payload.sessions) {
        sessions[scopeKey(session.agent_id, session.id)] = session
      }
      const tasks: Record<string, Task> = {}
      for (const task of payload.tasks || []) tasks[taskKey(task)] = task
      return {
        agents,
        disabledAgentConnections: payload.cursor >= state.cursor
          ? new Set(payload.disabled_agent_connections)
          : state.disabledAgentConnections,
        projects,
        sessions,
        tasks,
        notifications: {
          ...Object.fromEntries((payload.notifications || []).map(notice => [notice.key, notice])),
          ...state.notifications,
        },
        commands: payload.cursor >= state.cursor && payload.commands ? {
          ...Object.fromEntries(Object.entries(state.commands).filter(([, command]) => command.action !== 'enqueue' || !['queued', 'received', 'accepted', 'running', 'unknown', 'failed'].includes(command.state))),
          ...Object.fromEntries(payload.commands.map(command => [commandKey(command), command])),
        } : state.commands,
        // A delayed or old snapshot may never move the cursor backwards.
        approvals: payload.cursor >= state.cursor ? Object.fromEntries((payload.approvals || []).map(a => [a.id, a])) : state.approvals,
        cursor: Math.max(state.cursor, payload.cursor),
        resyncRequired: false,
      }
    }),

  mergeMessages: (agentId, sessionId, items, ahead) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = Object.values(state.messages[key] || {})
      const merged = mergeMessagesById(current, items)
      const nextMessages = { ...state.messages, [key]: messageMap(merged) }
      const nextCatchUps = historyCatchUps(state, key, items, ahead)
      return pruneInactiveMessageScopes(
        state,
        key,
        nextMessages,
        state.messageEventRevisions,
        nextCatchUps,
        state.messageStreams,
      )
    }),

  mergeMessagesPrepend: (agentId, sessionId, items, ahead) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = Object.values(state.messages[key] || {})
      const existingIds = new Set(current.map((item) => item.id))
      const incoming = new Map(items.map((item) => [item.id, item]))
      const prepended: Message[] = []
      const added = new Set<string>()
      for (const item of items) {
        if (!existingIds.has(item.id) && !added.has(item.id)) {
          added.add(item.id)
          prepended.push(item)
        }
      }
      const merged = [...prepended, ...current.map((item) => incoming.get(item.id) || item)]
      const nextMessages = { ...state.messages, [key]: messageMap(merged) }
      const nextCatchUps = historyCatchUps(state, key, items, ahead)
      return pruneInactiveMessageScopes(
        state,
        key,
        nextMessages,
        state.messageEventRevisions,
        nextCatchUps,
        state.messageStreams,
      )
    }),

  mergeCommands: (items) =>
    set((state) => {
      let next: Pick<AstrorderStore, 'commands' | 'outbox'> = {
        commands: state.commands,
        outbox: state.outbox,
      }
      for (const item of items) {
        next = mergeCommandIntoState({ ...state, ...next }, item)
      }
      return next
    }),

  mergeTasks: (items) =>
    set((state) => {
      const tasks = { ...state.tasks }
      const protectedScopes = new Set(sessionAccessOrder.slice(-6))
      for (const task of items) {
        const sKey = scopeKey(task.agent_id, task.session_id)
        protectedScopes.add(sKey)
        tasks[taskKey(task)] = { ...tasks[taskKey(task)], ...task }
      }
      return { tasks: pruneTaskMap(tasks, protectedScopes) }
    }),

  hydrateBotGroups: (items, revisionAtRequest) => set((state) => {
    const next = Object.fromEntries(items.map((group) => [group.id, group]))
    // A snapshot requested before a live update must not overwrite that update
    // or resurrect a group deleted while the request was in flight.
    for (const [id, revision] of Object.entries(state.botGroupEventRevisions)) {
      if (revision <= revisionAtRequest) continue
      if (state.botGroups[id]) next[id] = state.botGroups[id]
      else delete next[id]
    }
    return { botGroups: next }
  }),

  addNotification: (notification) => {
    let added = false
    set((state) => {
      if (state.notifications[notification.key]) return state
      added = true
      return { notifications: { ...state.notifications, [notification.key]: notification } }
    })
    return added
  },

  addOutbox: (command, status = command.state) =>
    set((state) => ({
      outbox: {
        ...state.outbox,
        [commandKey(command)]: { command, status, error: command.error },
      },
    })),

  updateOutboxAttachments: (agentId, sessionId, commandId, attachments) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId) + `::${commandId}`
      const current = state.outbox[key]
      if (!current) return state
      const command = { ...current.command, attachments }
      return {
        outbox: {
          ...state.outbox,
          [key]: { ...current, command },
        },
      }
    }),

  removeOutbox: (agentId, sessionId, commandId) =>
    set((storeState) => {
      const key = scopeKey(agentId, sessionId) + `::${commandId}`
      const next = { ...storeState.outbox }
      delete next[key]
      return { outbox: next }
          }),

  markOutboxError: (agentId, sessionId, commandId, error, status = 'unknown') =>
    set((storeState) => {
      const key = scopeKey(agentId, sessionId) + `::${commandId}`
      const current = storeState.outbox[key]
      if (!current) return storeState
      const commandState: CommandState = status === 'failed' ? 'failed' : 'unknown'
      const command = { ...current.command, state: commandState, error }
      return {
        outbox: {
          ...storeState.outbox,
          [key]: { ...current, command, status, error },
        },
      }
    }),

  applyEvent: (event) =>
    set((state) => {
      const eventKey = JSON.stringify([event.agent_id, event.id])
      if (!eventIsNew(eventKey, state.seenEventIds)) return state
      const seenEventIds = rememberEvent(state.seenEventIds, eventKey)
      const base = {
        seenEventIds,
        cursor: Math.max(state.cursor, event.cursor),
      }

      if (event.type === 'resync_required') {
        return { ...base, resyncRequired: true }
      }

      if (event.type === 'bot_group.upsert') {
        const group = event.data as unknown as BotGroup
        if (!group.id) return base
        const revision = state.botGroupRevision + 1
        return { ...base, botGroups: { ...state.botGroups, [group.id]: group }, botGroupRevision: revision, botGroupEventRevisions: { ...state.botGroupEventRevisions, [group.id]: revision } }
      }
      if (event.type === 'bot_group.delete') {
        const id = event.data.id
        const botGroups = { ...state.botGroups }
        if (typeof id !== 'string') return base
        delete botGroups[id]
        const revision = state.botGroupRevision + 1
        return { ...base, botGroups, botGroupRevision: revision, botGroupEventRevisions: { ...state.botGroupEventRevisions, [id]: revision } }
      }

      if (event.type === 'agent.upsert') {
        const agent = event.data as unknown as Agent
        if (!agent.id) return base
        return { ...base, agents: { ...state.agents, [agent.id]: agent } }
      }

      if (event.type === 'agent.connection_choice') {
        if (event.cursor <= state.cursor) return base
        const { key, enabled } = event.data
        if (typeof key !== 'string' || typeof enabled !== 'boolean') return base
        const disabledAgentConnections = new Set(state.disabledAgentConnections)
        if (enabled) disabledAgentConnections.delete(key)
        else disabledAgentConnections.add(key)
        return { ...base, disabledAgentConnections }
      }

      if (event.type === 'project.upsert') {
        const project = event.data as unknown as Project
        if (!project.id || !project.source_id || !project.project_id) return base
        return { ...base, projects: { ...state.projects, [project.id]: project } }
      }

      if (event.type === 'project.delete') {
        const data = event.data as {
          project_key?: string
          project_id?: string
          source_id?: string
          workspace?: string
          deleted_sessions?: Array<{ agent_id: string; id: string }>
        }
        const nextProjects = { ...state.projects }
        for (const [pKey, p] of Object.entries(nextProjects)) {
          if (
            pKey === data?.project_key ||
            p.id === data?.project_key ||
            (data?.project_id && p.project_id === data.project_id && (!data.source_id || p.source_id === data.source_id)) ||
            (data?.workspace && p.workspace === data.workspace)
          ) {
            delete nextProjects[pKey]
          }
        }
        const nextSessions = { ...state.sessions }
        for (const s of data?.deleted_sessions || []) {
          delete nextSessions[scopeKey(s.agent_id, s.id)]
        }
        return { ...base, projects: nextProjects, sessions: nextSessions }
      }

      if (event.type === 'session.upsert') {
        const session = event.data as unknown as Session
        if (!session.id || !session.agent_id) return base
        const key = scopeKey(session.agent_id, session.id)
        const prev = state.sessions[key]
        const mergedSession = Boolean(session.ephemeral) || Boolean(prev?.ephemeral)
          ? { ...session, ephemeral: true }
          : session
        return {
          ...base,
          sessions: { ...state.sessions, [key]: mergedSession },
          liveActivityAt: mergedSession.status === 'error'
            ? clearLiveActivity(state.liveActivityAt, key)
            : mergedSession.status === 'idle'
              ? settleLiveActivityOnIdle(state, key, prev)
              : state.liveActivityAt,
        }
      }

      if (event.type === 'session.delete') {
        const data = event.data as { id?: string; agent_id?: string }
        const delSessionId = data?.id || event.session_id
        const delAgentId = data?.agent_id || event.agent_id
        if (!delSessionId || !delAgentId) return base
        const key = scopeKey(delAgentId, delSessionId)
        const nextSessions = { ...state.sessions }
        delete nextSessions[key]
        return { ...base, sessions: nextSessions }
      }

      if (event.type === 'message.upsert' || event.type === 'message.delta') {
        return { ...base, ...liveMessageUpdate(state, event) }
      }

      if (event.type === 'command.upsert') {
        const command = event.data as unknown as Command
        if (!command.id || !command.session_id || !command.agent_id) return base
        const next = mergeCommandIntoState({ ...state, ...base }, command)
        const key = scopeKey(command.agent_id, command.session_id)
        const shouldClear = command.state === 'cancelled' || command.state === 'failed'
        return {
          ...base,
          ...next,
          ...(shouldClear ? { liveActivityAt: clearLiveActivity(state.liveActivityAt, key) } : {}),
        }
      }

      if (event.type === 'task.upsert' || event.type === 'task.live') {
        const task = event.data as unknown as Task
        if (!task.id || !task.session_id || !task.agent_id) return base
        const key = taskKey(task)
        const sessionKey = scopeKey(task.agent_id, task.session_id)
        const protectedScopes = new Set(sessionAccessOrder.slice(-6))
        protectedScopes.add(sessionKey)
        const nextTasks = pruneTaskMap(
          { ...state.tasks, [key]: { ...state.tasks[key], ...task } },
          protectedScopes,
        )
        return {
          ...base,
          tasks: nextTasks,
          liveActivityAt: task.progress?.blocking === false || !['running', 'waiting_approval'].includes(task.status)
            ? state.liveActivityAt : touchLiveActivity(state.liveActivityAt, sessionKey),
        }
      }

      if (event.type === 'approval.upsert') {
        const approval = normalizeApproval(event)
        if (!approval) return base
        return { ...base, approvals: { ...state.approvals, [approval.id]: approval } }
      }

      return base
    }),

  applyMessageDeltas: (events) =>
    set((state) => {
      const seen = new Set(Object.keys(state.seenEventIds))
      const groups = new Map<string, { event: EventEnvelope; fragments: string[] }>()
      let received = false
      for (const event of events) {
        const eventKey = JSON.stringify([event.agent_id, event.id])
        if (seen.has(eventKey)) continue
        seen.add(eventKey)
        received = true
        const message = event.data as unknown as Message
        if (!message.id || !message.agent_id || !message.session_id) continue
        const key = JSON.stringify([message.agent_id, message.session_id, message.id])
        const group = groups.get(key)
        if (!group) {
          groups.set(key, { event, fragments: [message.text] })
          continue
        }
        const previous = group.event.data
        const data = { ...previous, ...event.data }
        if (data.timestamp_source !== 'native') {
          data.created_at = previous.created_at
          data.timestamp_source = previous.timestamp_source
          data.completed_at = previous.completed_at
        }
        data.command_id = data.command_id || previous.command_id
        data.turn_id = data.turn_id || previous.turn_id
        group.event = { ...group.event, data }
        group.fragments.push(message.text)
      }
      if (!received) return state
      let next = state
      for (const { event, fragments } of groups.values()) {
        const update = liveMessageUpdate(next, { ...event, data: { ...event.data, text: fragments.join('') } })
        if (update) next = { ...next, ...update }
      }
      return {
        ...next,
        seenEventIds: Object.fromEntries([...seen].slice(-MAX_SEEN_EVENT_IDS).map(id => [id, true])),
      }
    }),

  setDraft: (agentId, sessionId, draft) =>
    set((state) => ({ drafts: { ...state.drafts, [scopeKey(agentId, sessionId)]: draft } })),

  setDraftText: (agentId, sessionId, text) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = state.drafts[key] || emptyDraft()
      return { drafts: { ...state.drafts, [key]: { ...current, text } } }
    }),
  updateSession: (agentId, sessionId, patch) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = state.sessions[key]
      if (!current) return state
      return { sessions: { ...state.sessions, [key]: { ...current, ...patch } } }
    }),

  commitOptimisticSend: ({ command, optimisticMessage, clearDraft = false, markRunning = false }) =>
    set((state) => {
      const key = scopeKey(command.agent_id, command.session_id)
      const patch: Partial<AstrorderStore> = {
        outbox: {
          ...state.outbox,
          [commandKey(command)]: { command, status: 'submitting', error: command.error },
        },
      }
      if (optimisticMessage) {
        const current = Object.values(state.messages[key] || {})
        const merged = mergeMessagesById(current, [optimisticMessage])
        const nextMessages = { ...state.messages, [key]: messageMap(merged) }
        const nextCatchUps = historyCatchUps(state, key, [optimisticMessage])
        Object.assign(
          patch,
          pruneInactiveMessageScopes(
            state,
            key,
            nextMessages,
            state.messageEventRevisions,
            nextCatchUps,
            state.messageStreams,
          ),
        )
      }
      if (clearDraft) {
        patch.drafts = { ...state.drafts, [key]: emptyDraft() }
      }
      if (markRunning && state.sessions[key]) {
        patch.sessions = {
          ...state.sessions,
          [key]: { ...state.sessions[key], status: 'running' },
        }
      }
      return patch
    }),

  resetRuntime: () => {
    scheduleActivityExpiry({})
    sessionAccessOrder = []
    set({
      agents: {},
      disabledAgentConnections: new Set(),
      projects: {},
      sessions: {},
      messages: {},
      messageRevision: 0,
      messageEventRevisions: {},
      messageCatchUps: {},
      messageStreams: {},
      commands: {},
      tasks: {},
      approvals: {},
      notifications: {},
      botGroups: {},
      botGroupRevision: 0,
      botGroupEventRevisions: {},
      outbox: {},
      drafts: {},
      liveActivityAt: {},
      cursor: 0,
      seenEventIds: {},
      connection: 'disconnected',
      resyncRequired: false,
    })
  },
}))

export function isSessionAgentDisconnected(state: AstrorderStore, agentId: string): boolean {
  const agent = state.agents[agentId]
  if (!agent) return false
  const kind = agent.kind === 'antigravity-ssh' ? 'antigravity' : agent.kind
  return state.disabledAgentConnections.has(`${agent.connection_id || 'local'}:${kind}`)
}

export function isDisconnectedSessionRoute(state: AstrorderStore, sessionId: string, agentId: string | null): boolean {
  if (agentId) return isSessionAgentDisconnected(state, agentId)
  if (!state.disabledAgentConnections.size) return false
  const candidates = Object.values(state.sessions).filter(session => session.id === sessionId && !isEphemeralSession(session))
  return candidates.length === 1 && isSessionAgentDisconnected(state, candidates[0].agent_id)
}

export function selectSessions(state: AstrorderStore): Session[] {
  return Object.values(state.sessions)
    .filter(session => !isEphemeralSession(session) && !isSessionAgentDisconnected(state, session.agent_id))
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
}

export function selectProjects(state: AstrorderStore): Project[] {
  return Object.values(state.projects).sort(
    (a, b) => b.updated_at.localeCompare(a.updated_at) || (a.project_name || '').localeCompare(b.project_name || '') || a.id.localeCompare(b.id),
  )
}

export function selectMessages(state: AstrorderStore, agentId: string, sessionId: string): Message[] {
  return Object.values(state.messages[scopeKey(agentId, sessionId)] || {})
}

export function selectCommands(state: AstrorderStore, agentId: string, sessionId: string): Command[] {
  return Object.values(state.commands).filter(
    (command) => command.agent_id === agentId && command.session_id === sessionId,
  )
}

export function selectTasks(state: AstrorderStore, agentId: string, sessionId: string): Task[] {
  return Object.values(state.tasks)
    .filter((task) => task.agent_id === agentId && task.session_id === sessionId)
    .sort((a, b) => b.updated_at.localeCompare(a.updated_at))
}

export function selectOutbox(state: AstrorderStore, agentId: string, sessionId: string): OutboxEntry[] {
  return Object.values(state.outbox)
    .filter(
      (entry) =>
        entry.command.agent_id === agentId && entry.command.session_id === sessionId,
    )
    .sort((a, b) => a.command.created_at.localeCompare(b.command.created_at))
}

export function selectApprovals(state: AstrorderStore, agentId: string, sessionId: string): Approval[] {
  return Object.values(state.approvals).filter(
    (approval) => approval.agent_id === agentId && approval.session_id === sessionId && approval.state === 'pending',
  )
}
