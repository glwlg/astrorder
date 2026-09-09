import { expect, it } from 'vitest'
import { reconcileSessionOrder } from './useSessionOrder'
import type { Session } from '../domain/types'
const row = (id: string, updated_at: string): Session => ({ id, agent_id: 'a', title: id, workspace: null, status: 'idle', updated_at })
it('sorts initially and on submit only, never on runtime updates', () => {
  const rows = [row('old', '2026-01-01'), row('new', '2026-02-01')]
  const initial = reconcileSessionOrder([], rows, false)
  const changed = [row('old', '2026-03-01'), rows[1]]
  expect(reconcileSessionOrder(initial, changed, false)).toEqual(initial)
  expect(reconcileSessionOrder(initial, changed, true)).toEqual([...initial].reverse())
})
