import type { Message, Session } from './types'

export function isEphemeralSession(session: Pick<Session, 'ephemeral' | 'title'>): boolean {
  // Exact marker emitted by older side-chat builds that lost the wire flag.
  // Do not hide ordinary titles merely discussing side chat.
  return session.ephemeral === true || session.title === '[侧边聊天]'
}

export function scopeKey(agentId: string, sessionId: string): string {
  return `${agentId}::${sessionId}`
}

export function mergeMessagesById<T extends { id: string; role?: string; text?: string; command_id?: string | null; created_at?: string }>(existing: T[], incoming: T[]): T[] {
  const result: T[] = []
  const positions = new Map<string, number>()
  const commandPositions = new Map<string, number>()

  for (const item of [...existing, ...incoming]) {
    if (!positions.has(item.id) && item.role === 'user' && item.command_id) {
      const correlatedIdx = commandPositions.get(item.command_id)
      if (correlatedIdx !== undefined) {
        const current = result[correlatedIdx]
        if (current.id.startsWith('optimistic-') || !current.created_at || !item.created_at || item.created_at >= current.created_at) {
          positions.delete(current.id)
          positions.set(item.id, correlatedIdx)
          result[correlatedIdx] = item
        }
        continue
      }
    }

    const position = positions.get(item.id)
    if (position === undefined) {
      positions.set(item.id, result.length)
      if (item.role === 'user' && item.command_id) commandPositions.set(item.command_id, result.length)
      result.push(item)
    } else {
      result[position] = item
    }
  }

  return result
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
