import { expect, it, vi } from 'vitest'
import type { Message } from './types'
import { loadActivityHistory, refreshActivityHistory } from './loadActivityHistory'
import { activityPacks } from './activityPacks'

const tool = (id: string): Message => ({ id, agent_id: 'a', session_id: 's', role: 'tool', kind: 'tool', tool: { name: 'terminal' }, text: '', attachments: [], created_at: '1970-01-01T00:00:00Z', command_id: null })

it('stops at native prose and compaction instead of pulling an entire long turn', async () => {
  const read = vi.fn().mockResolvedValueOnce({ items: [tool('new')], next_cursor: 'older' }).mockResolvedValueOnce({ items: [{ ...tool('compact'), tool: { name: 'contextCompaction' } }, tool('older')], next_cursor: 'previous' })
  const page = await loadActivityHistory(read, undefined, 50)
  expect(page.items.map(m => m.id)).toEqual(['compact', 'older', 'new'])
  expect(page.next_cursor).toBe('previous')
  expect(read).toHaveBeenCalledTimes(2)
})

it('ignores empty native placeholders when completing a group across short pages', async () => {
  const rows = Array.from({ length: 100 }, (_, i) => tool(String(i)))
  const read = vi.fn().mockResolvedValueOnce({ items: rows.slice(50), next_cursor: 'a' }).mockResolvedValueOnce({ items: [{ ...tool('empty'), role: 'system', kind: 'message', tool: null }, ...rows.slice(25, 50)], next_cursor: 'b' }).mockResolvedValueOnce({ items: rows.slice(0, 25), next_cursor: 'c' })
  const page = await loadActivityHistory(read, undefined, 50)
  expect(page.completeActivityGroup).toBe(true)
  expect(read.mock.calls.map(call => call[1])).toEqual([50, 50, 25])
  expect(page.next_cursor).toBe('c')
})

it('rejects repeated cursors and preserves a failed partial group as an error', async () => {
  const stuck = vi.fn().mockResolvedValue({ items: [tool('t')], next_cursor: 'same' })
  await expect(loadActivityHistory(stuck, undefined, 50)).rejects.toThrow('游标未前进')
  const failed = vi.fn().mockResolvedValueOnce({ items: [tool('t')], next_cursor: 'older' }).mockRejectedValueOnce(new Error('offline'))
  await expect(loadActivityHistory(failed, undefined, 50)).rejects.toThrow('offline')
})

it('keeps existing groups and their first-message identities when older history is prepended', () => {
  const rows = Array.from({ length: 230 }, (_, i) => tool(String(i)))
  const before = [...activityPacks(rows.slice(130), ['130']).values()]
  const after = [...activityPacks(rows, ['30', '130']).values()]
  expect(after.map(pack => pack.length)).toEqual([30, 100, 100])
  expect(after[2]).toEqual(before[0])
  expect(new Set(after.flat().map(m => m.id)).size).toBe(230)
})

it('refreshActivityHistory reuses cached older prefix on overlap in a single read and falls back when disjoint', async () => {
  const older = Array.from({ length: 60 }, (_, i) => tool(`t-${i}`))
  const previous = { items: older, next_cursor: 'cursor-older', completeActivityGroup: false }
  const freshWindow = [...older.slice(20), tool('t-60'), tool('t-61')]
  const readOverlap = vi.fn().mockResolvedValueOnce({ items: freshWindow, next_cursor: 'cursor-mid' })

  const refreshed = await refreshActivityHistory(readOverlap, previous, 50)
  expect(readOverlap).toHaveBeenCalledTimes(1)
  expect(refreshed.next_cursor).toBe('cursor-older')
  expect(refreshed.items.map(m => m.id)).toEqual([...older.map(m => m.id), 't-60', 't-61'])

  // Disjoint fresh window (>50 brand-new items) falls back to multi-page completion
  const readDisjoint = vi.fn()
    .mockResolvedValueOnce({ items: [tool('new-1')], next_cursor: 'c1' })
    .mockResolvedValueOnce({ items: [{ ...tool('compact'), tool: { name: 'contextCompaction' } }], next_cursor: 'c2' })
  const fallback = await refreshActivityHistory(readDisjoint, previous, 50)
  expect(readDisjoint).toHaveBeenCalledTimes(2)
  expect(fallback.next_cursor).toBe('c2')
})
