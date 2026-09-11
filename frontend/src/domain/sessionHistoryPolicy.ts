export const INITIAL_HISTORY_LIMIT = 50
export const OLDER_HISTORY_LIMIT = 30
export const MAX_AUTO_BACKFILL_PAGES = 4
export const MAX_AUTO_BACKFILL_ITEMS = 200

export function needsUserTurnBackfill(params: {
  items: Array<{ role: string }>
  hasNextPage: boolean
  autoFetchedPages: number
  maxAutoPages?: number
  maxItems?: number
}): boolean {
  if (!params.hasNextPage) return false
  if (params.autoFetchedPages >= (params.maxAutoPages ?? MAX_AUTO_BACKFILL_PAGES)) return false
  if (params.items.length >= (params.maxItems ?? MAX_AUTO_BACKFILL_ITEMS)) return false
  return !params.items.some((item) => item.role === 'user')
}
