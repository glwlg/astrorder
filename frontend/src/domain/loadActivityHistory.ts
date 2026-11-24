import type { Message } from './types'
import { hasMessageContent, normalizeMessagePresentation } from './messagePresentation'
import { ACTIVITY_PACK_LIMIT, isPackActivity } from './activityPacks'
import { isCompactionMessage } from '../features/chat/CompactionDivider'

export type HistoryPage = { items: Message[]; next_cursor: string | null }
export const visibleHistoryItems = (items: Message[]) => items.map(normalizeMessagePresentation).filter(item => isCompactionMessage(item) || hasMessageContent(item))

const MAX_EXTRA_ACTIVITY_PAGES = 3
const MAX_STITCHED_ITEMS = 250

function leadingActivityCount(items: Message[]): number {
  const visible = visibleHistoryItems(items)
  let count = 0
  while (count < visible.length && isPackActivity(visible[count])) count++
  return count
}

async function completeActivityHistoryFromFirstPage(
  read: (before: string | undefined, limit: number) => Promise<HistoryPage>,
  firstPage: HistoryPage,
  before: string | undefined,
): Promise<HistoryPage & { completeActivityGroup: boolean }> {
  let page = firstPage
  const seen = new Set<string>(before ? [before] : [])
  let extraPages = 0
  while (page.next_cursor && extraPages < MAX_EXTRA_ACTIVITY_PAGES) {
    const count = leadingActivityCount(page.items)
    if (!count || count % ACTIVITY_PACK_LIMIT === 0) break
    const cursor = page.next_cursor
    if (seen.has(cursor)) throw new Error('历史分页游标未前进，请重试')
    seen.add(cursor)
    extraPages++
    const older = await read(cursor, ACTIVITY_PACK_LIMIT - count % ACTIVITY_PACK_LIMIT)
    const unique = new Map(older.items.map(item => [item.id, item]))
    for (const item of page.items) unique.set(item.id, item)
    page = { items: [...unique.values()], next_cursor: older.next_cursor }
  }
  return { ...page, completeActivityGroup: leadingActivityCount(page.items) >= ACTIVITY_PACK_LIMIT }
}

/** Publish only after the leading activity group is complete or reaches a native boundary. */
export async function loadActivityHistory(
  read: (before: string | undefined, limit: number) => Promise<HistoryPage>,
  before: string | undefined,
  limit: number,
): Promise<HistoryPage & { completeActivityGroup: boolean }> {
  const firstPage = await read(before, limit)
  return completeActivityHistoryFromFirstPage(read, firstPage, before)
}

/**
 * Refresh the latest window of an already-loaded session in a single HTTP request
 * whenever the fresh page overlaps the previously loaded items, reusing older
 * leading activity items instead of re-fetching up to 3 extra older pages.
 */
export async function refreshActivityHistory(
  read: (before: string | undefined, limit: number) => Promise<HistoryPage>,
  previous: (HistoryPage & { completeActivityGroup: boolean }) | undefined,
  limit: number,
): Promise<HistoryPage & { completeActivityGroup: boolean }> {
  const firstPage = await read(undefined, limit)
  if (!firstPage.next_cursor) {
    return { ...firstPage, completeActivityGroup: leadingActivityCount(firstPage.items) >= ACTIVITY_PACK_LIMIT }
  }
  if (previous && previous.items.length > 0 && previous.items.length <= MAX_STITCHED_ITEMS && firstPage.items.length > 0) {
    const freshIds = new Set(firstPage.items.map(item => item.id))
    const overlapIdx = previous.items.findIndex(item => freshIds.has(item.id))
    if (overlapIdx >= 0) {
      const olderPrefix = previous.items.slice(0, overlapIdx)
      const combined = [...olderPrefix, ...firstPage.items]
      const nextCursor = overlapIdx > 0 ? previous.next_cursor : firstPage.next_cursor
      return {
        items: combined,
        next_cursor: nextCursor,
        completeActivityGroup: leadingActivityCount(combined) >= ACTIVITY_PACK_LIMIT,
      }
    }
  }
  return completeActivityHistoryFromFirstPage(read, firstPage, undefined)
}
