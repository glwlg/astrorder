import type { Message } from './types'
import { isMessageActivity, normalizeMessagePresentation } from './messagePresentation'
import { isCompactionMessage } from '../features/chat/CompactionDivider'

export const ACTIVITY_PACK_LIMIT = 100

export function isPackActivity(message: Message | null | undefined): boolean {
  return Boolean(message && !isCompactionMessage(message) && isMessageActivity(normalizeMessagePresentation(message)))
}

/** Native page boundaries remain visible when older history is prepended. */
export function activityPacks(messages: Message[], breaks: readonly string[] = []): Map<number, Message[]> {
  const boundaries = new Set(breaks)
  const packs = new Map<number, Message[]>()
  let pack: Message[] | undefined
  for (let i = 0; i < messages.length; i++) {
    const message = messages[i]
    if (!isPackActivity(message)) { pack = undefined; continue }
    if (!pack || pack.length === ACTIVITY_PACK_LIMIT || boundaries.has(message.id)) {
      pack = []
      packs.set(i, pack)
    }
    pack.push(message)
  }
  return packs
}
