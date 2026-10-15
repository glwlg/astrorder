import type { Message, Session } from './types'

export function isEphemeralSession(session: Pick<Session, 'ephemeral' | 'title'>): boolean {
  // Exact marker emitted by older side-chat builds that lost the wire flag.
  // Do not hide ordinary titles merely discussing side chat.
  return session.ephemeral === true || session.title === '[侧边聊天]'
}

export function scopeKey(agentId: string, sessionId: string): string {
  return `${agentId}::${sessionId}`
}

export function findSessionHandoff(selected: Session | null, sessions: Session[]): { session: Session; direction: 'from' | 'to' } | null {
  if (!selected) return null
  // These IDs also encode scheduling/parentage. Only a saved handoff context
  // establishes that another session actually took over the conversation.
  const isHandoff = (session: Session) => Boolean(
    session.handoff_from_agent_id && session.handoff_from_session_id && session.handoff_context?.trim(),
  )
  if (isHandoff(selected)) {
    const source = sessions.find(session =>
      session.agent_id === selected.handoff_from_agent_id && session.id === selected.handoff_from_session_id,
    )
    if (source) return { session: source, direction: 'from' }
  }
  const target = sessions.find(session => isHandoff(session)
    && session.handoff_from_agent_id === selected.agent_id && session.handoff_from_session_id === selected.id,
  )
  return target ? { session: target, direction: 'to' } : null
}

type ReconciledMessage = {
  id: string
  role?: string
  text?: string
  command_id?: string | null
  created_at?: string
  attachments?: { id: string; name: string; media_type: string }[]
}

/** SQLite native timestamps are UTC even when their wire format omits the zone. */
function messageTime(value = ''): number {
  return Date.parse(/^\d{4}-\d{2}-\d{2} \d{2}:/.test(value) ? value.replace(' ', 'T') + 'Z' : value)
}

/** Local input expires once populated native user history covers its send time. */
export function confirmsOptimisticUser(optimistic: ReconciledMessage, native: ReconciledMessage): boolean {
  if (!optimistic.id.startsWith('optimistic-') || optimistic.role !== 'user' || native.role !== 'user' || native.id.startsWith('optimistic-')) return false
  if (!native.text?.trim() && !native.attachments?.length) return false
  const submittedAt = messageTime(optimistic.created_at)
  const persistedAt = messageTime(native.created_at)
  return Number.isFinite(submittedAt) && Number.isFinite(persistedAt) && persistedAt >= submittedAt
}

export function mergeMessagesById<T extends ReconciledMessage>(existing: T[], incoming: T[]): T[] {
  const result: T[] = []
  const positions = new Map<string, number>()
  const assistantPositions = new Map<string, number>()

  for (const item of [...existing, ...incoming]) {
    if (item.role === 'assistant' && item.command_id) {
      const correlatedIdx = assistantPositions.get(item.command_id)
      if (correlatedIdx !== undefined) {
        const current = result[correlatedIdx]
        if (current.id !== item.id && (current.id.startsWith('hermes-stream-') || item.id.startsWith('hermes-stream-'))) {
          if (!item.id.startsWith('hermes-stream-')) {
            positions.delete(current.id)
            positions.set(item.id, correlatedIdx)
            result[correlatedIdx] = item
          }
          continue
        }
      }
    }
    const position = positions.get(item.id)
    if (position === undefined) {
      positions.set(item.id, result.length)
      if (item.role === 'assistant' && item.command_id) assistantPositions.set(item.command_id, result.length)
      result.push(item)
    } else {
      result[position] = item
    }
  }

  // A row may first arrive empty and later update under the same ID.
  // Reconcile after all updates so the completed row replaces its placeholder too.
  const nativeUsers = result.filter(item => item.role === 'user' && !item.id.startsWith('optimistic-'))
  return result.filter(item => !item.id.startsWith('optimistic-') || !nativeUsers.some(native => confirmsOptimisticUser(item, native)))
}

export function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  return mergeMessagesById(existing, incoming)
}

export function isDraftSendable(text: string, attachments: Array<unknown>, extras: Array<unknown> = []): boolean {
  return text.trim().length > 0 || attachments.length > 0 || extras.length > 0
}

export function isNearBottom({
  scrollTop,
  clientHeight,
  scrollHeight,
  threshold = 28,
}: {
  scrollTop: number
  clientHeight: number
  scrollHeight: number
  threshold?: number
}): boolean {
  return scrollHeight - (scrollTop + clientHeight) <= threshold
}

export function eventIsNew(eventId: string, seen: Record<string, true>): boolean {
  const id = eventId.trim()
  return id.length > 0 && seen[id] !== true
}

export function newCommandId(): string {
  if (typeof globalThis.crypto?.randomUUID === 'function') return globalThis.crypto.randomUUID()
  const bytes = new Uint8Array(16)
  globalThis.crypto?.getRandomValues?.(bytes)
  return [...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('')
}

export function safeText(value: unknown, fallback = ''): string {
  return typeof value === 'string' ? value : fallback
}
