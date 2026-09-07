import type { Message } from './types'

export function scopeKey(agentId: string, sessionId: string): string {
  return `${agentId}::${sessionId}`
}

export function mergeMessagesById<T extends { id: string }>(existing: T[], incoming: T[]): T[] {
  const result = [...existing]
  const positions = new Map(result.map((item, index) => [item.id, index]))

  for (const item of incoming) {
    const position = positions.get(item.id)
    if (position === undefined) {
      positions.set(item.id, result.length)
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

export function isDraftSendable(text: string, attachments: Array<unknown>): boolean {
  return text.trim().length > 0 || attachments.length > 0
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
