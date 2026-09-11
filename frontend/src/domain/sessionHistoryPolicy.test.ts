import { expect, it } from 'vitest'
import { MAX_AUTO_BACKFILL_PAGES, needsUserTurnBackfill } from './sessionHistoryPolicy'

it('does not backfill when the newest page already includes a user turn', () => {
  expect(needsUserTurnBackfill({
    items: [{ role: 'user' }, { role: 'assistant' }, { role: 'tool' }],
    hasNextPage: true,
    autoFetchedPages: 0,
  })).toBe(false)
})

it('backfills while the visible window is only tool/assistant noise after the last user turn', () => {
  expect(needsUserTurnBackfill({
    items: [{ role: 'assistant' }, { role: 'tool' }, { role: 'tool' }],
    hasNextPage: true,
    autoFetchedPages: 0,
  })).toBe(true)
})

it('stops when history is exhausted or the page cap is reached', () => {
  expect(needsUserTurnBackfill({
    items: [{ role: 'assistant' }],
    hasNextPage: false,
    autoFetchedPages: 0,
  })).toBe(false)
  expect(needsUserTurnBackfill({
    items: [{ role: 'assistant' }],
    hasNextPage: true,
    autoFetchedPages: MAX_AUTO_BACKFILL_PAGES,
  })).toBe(false)
})
