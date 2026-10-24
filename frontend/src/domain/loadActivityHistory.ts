import type { Message } from './types'
import { hasMessageContent, normalizeMessagePresentation } from './messagePresentation'
import { ACTIVITY_PACK_LIMIT, isPackActivity } from './activityPacks'
import { isCompactionMessage } from '../features/chat/CompactionDivider'

export type HistoryPage = { items: Message[]; next_cursor: string | null }
export const visibleHistoryItems = (items: Message[]) => items.map(normalizeMessagePresentation).filter(item => isCompactionMessage(item) || hasMessageContent(item))

/** Publish only after the leading activity group is complete or reaches a native boundary. */
export async function loadActivityHistory(
  read: (before: string | undefined, limit: number) => Promise<HistoryPage>,
  before: string | undefined,
  limit: number,
): Promise<HistoryPage & { completeActivityGroup: boolean }> {
  let page = await read(before, limit)
  const seen = new Set<string>(before ? [before] : [])
  while (page.next_cursor) {
    const visible = visibleHistoryItems(page.items)
    let count = 0
    while (count < visible.length && isPackActivity(visible[count])) count++
    if (!count || count % ACTIVITY_PACK_LIMIT === 0) break
    const cursor = page.next_cursor
    if (seen.has(cursor)) throw new Error('历史分页游标未前进，请重试')
    seen.add(cursor)
    const older = await read(cursor, ACTIVITY_PACK_LIMIT - count % ACTIVITY_PACK_LIMIT)
    const unique = new Map(older.items.map(item => [item.id, item]))
    for (const item of page.items) unique.set(item.id, item)
    page = { items: [...unique.values()], next_cursor: older.next_cursor }
  }
  const visible = visibleHistoryItems(page.items)
  let count = 0
  while (count < visible.length && isPackActivity(visible[count])) count++
  return { ...page, completeActivityGroup: count >= ACTIVITY_PACK_LIMIT }
}
