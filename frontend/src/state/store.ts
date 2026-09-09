import { create } from 'zustand'
import { eventIsNew, mergeMessagesById, scopeKey } from '../domain/semantics'
import type {
  Agent,
  Approval,
  BootstrapPayload,
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
  projects: Record<string, Project>
  sessions: Record<string, Session>
  messages: Record<string, Record<string, Message>>
  commands: Record<string, Command>
  tasks: Record<string, Task>
  approvals: Record<string, Approval>
  notifications: Record<string, EventNotification>
  outbox: Record<string, OutboxEntry>
  drafts: Record<string, DraftState>
  cursor: number
  seenEventIds: Record<string, true>
  connection: ConnectionStatus
  resyncRequired: boolean
  setConnection: (connection: ConnectionStatus) => void
  hydrateBootstrap: (payload: BootstrapPayload) => void
  mergeMessages: (agentId: string, sessionId: string, items: Message[]) => void
  mergeMessagesPrepend: (agentId: string, sessionId: string, items: Message[]) => void
  mergeCommands: (items: Command[]) => void
  mergeTasks: (items: Task[]) => void
  addNotification: (notification: EventNotification) => boolean
  addOutbox: (command: Command, status?: OutboxStatus) => void
  updateOutboxAttachments: (
    agentId: string,
    sessionId: string,
    commandId: string,
    attachments: Command['attachments'],
  ) => void
  markOutboxError: (
    agentId: string,
    sessionId: string,
    commandId: string,
    error: string,
    status?: OutboxStatus,
  ) => void
  applyEvent: (event: EventEnvelope) => void
  setDraft: (agentId: string, sessionId: string, draft: DraftState) => void
  setDraftText: (agentId: string, sessionId: string, text: string) => void
  resetRuntime: () => void
}

const emptyDraft = (): DraftState => ({ text: '', attachments: [] })

function commandKey(command: Command): string {
  return scopeKey(command.agent_id, command.session_id) + `::${command.id}`
}

function taskKey(task: Task): string {
  return scopeKey(task.agent_id, task.session_id) + `::${task.id}`
}

function messageMap(items: Message[]): Record<string, Message> {
  return Object.fromEntries(items.map((item) => [item.id, item]))
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
  projects: {},
  sessions: {},
  messages: {},
  commands: {},
  tasks: {},
  approvals: {},
  notifications: {},
  outbox: {},
  drafts: {},
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
        projects,
        sessions,
        tasks,
        // A delayed or old snapshot may never move the cursor backwards.
        cursor: Math.max(state.cursor, payload.cursor),
        resyncRequired: false,
      }
    }),

  mergeMessages: (agentId, sessionId, items) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = Object.values(state.messages[key] || {})
      const merged = mergeMessagesById(current, items)
      return { messages: { ...state.messages, [key]: messageMap(merged) } }
    }),

  mergeMessagesPrepend: (agentId, sessionId, items) =>
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
      return { messages: { ...state.messages, [key]: messageMap(merged) } }
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
      for (const task of items) tasks[taskKey(task)] = { ...tasks[taskKey(task)], ...task }
      return { tasks }
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
      const seenEventIds: Record<string, true> = { ...state.seenEventIds, [eventKey]: true }
      const base = {
        seenEventIds,
        cursor: Math.max(state.cursor, event.cursor),
      }

      if (event.type === 'resync_required') {
        return { ...base, resyncRequired: true }
      }

      if (event.type === 'agent.upsert') {
        const agent = event.data as unknown as Agent
        if (!agent.id) return base
        return { ...base, agents: { ...state.agents, [agent.id]: agent } }
      }

      if (event.type === 'project.upsert') {
        const project = event.data as unknown as Project
        if (!project.id || !project.source_id || !project.project_id) return base
        return { ...base, projects: { ...state.projects, [project.id]: project } }
      }

      if (event.type === 'session.upsert') {
        const session = event.data as unknown as Session
        if (!session.id || !session.agent_id) return base
        const key = scopeKey(session.agent_id, session.id)
        return { ...base, sessions: { ...state.sessions, [key]: session } }
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

      if (event.type === 'message.upsert') {
        const message = event.data as unknown as Message
        if (!message.id || !message.session_id || !message.agent_id) return base
        const key = scopeKey(message.agent_id, message.session_id)
        const current = Object.values(state.messages[key] || {})
        const merged = mergeMessagesById(current, [message])
        return { ...base, messages: { ...state.messages, [key]: messageMap(merged) } }
      }

      if (event.type === 'command.upsert') {
        const command = event.data as unknown as Command
        if (!command.id || !command.session_id || !command.agent_id) return base
        const next = mergeCommandIntoState({ ...state, ...base }, command)
        return { ...base, ...next }
      }

      if (event.type === 'task.upsert') {
        const task = event.data as unknown as Task
        if (!task.id || !task.session_id || !task.agent_id) return base
        const key = taskKey(task)
        return { ...base, tasks: { ...state.tasks, [key]: { ...state.tasks[key], ...task } } }
      }

      if (event.type === 'approval.upsert') {
        const approval = normalizeApproval(event)
        if (!approval) return base
        return { ...base, approvals: { ...state.approvals, [approval.id]: approval } }
      }

      return base
    }),

  setDraft: (agentId, sessionId, draft) =>
    set((state) => ({ drafts: { ...state.drafts, [scopeKey(agentId, sessionId)]: draft } })),

  setDraftText: (agentId, sessionId, text) =>
    set((state) => {
      const key = scopeKey(agentId, sessionId)
      const current = state.drafts[key] || emptyDraft()
      return { drafts: { ...state.drafts, [key]: { ...current, text } } }
    }),

  resetRuntime: () =>
    set({
      agents: {},
      projects: {},
      sessions: {},
      messages: {},
      commands: {},
      tasks: {},
      approvals: {},
      notifications: {},
      outbox: {},
      drafts: {},
      cursor: 0,
      seenEventIds: {},
      connection: 'disconnected',
      resyncRequired: false,
    }),
}))

export function selectSessions(state: AstrorderStore): Session[] {
  return Object.values(state.sessions).sort((a, b) => b.updated_at.localeCompare(a.updated_at))
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
