import type { Message } from './types'

/** Older Codex projections stored native item types in the display kind. */
export function normalizeMessagePresentation(message: Message): Message {
  const kind = String(message.kind)
  if (kind === 'reasoning') return { ...message, role: 'assistant', kind: 'thinking' }
  if (kind === 'agentMessage' || kind === 'plan') return { ...message, role: 'assistant', kind: 'message' }
  if (kind === 'userMessage') return { ...message, role: 'user', kind: 'message' }
  return message
}

export function isMessageActivity(message: Message | null): boolean {
  return Boolean(message && (message.kind === 'thinking' || message.kind === 'tool' || message.role === 'tool'))
}

export function hasMessageContent(message: Message): boolean {
  return Boolean(message.text.trim() || message.attachments.length || message.tool)
}
