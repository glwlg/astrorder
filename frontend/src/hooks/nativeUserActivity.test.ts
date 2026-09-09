import { act, renderHook } from '@testing-library/react'
import { expect, it } from 'vitest'
import type { Session } from '../domain/types'
import { nativeActivityEvent, useSessionOrder } from './useSessionOrder'
const session = (id: string, updated_at: string): Session => ({ id, updated_at, agent_id: 'h', title: id, workspace: '/work', status: 'idle' })
it('reorders for native user activity once and ignores replies and replayed snapshots', () => {
 const rows = [session('current', '2026-09-06T00:00:00Z'), session('other', '2026-09-08T00:00:00Z')]
 const hook = renderHook(({ rows }) => useSessionOrder(rows), { initialProps: { rows } })
 expect(hook.result.current.map(s => s.id)).toEqual(['other', 'current'])
 const activity = [{ agent_id: 'h', id: 'current', last_user_at: '2026-09-09T04:00:00Z' }]
 act(() => window.dispatchEvent(new CustomEvent(nativeActivityEvent, { detail: activity })))
 expect(hook.result.current.map(s => s.id)).toEqual(['current', 'other'])
 expect(hook.result.current[0].updated_at).toBe(activity[0].last_user_at)
 hook.rerender({ rows: [rows[0], { ...rows[1], updated_at: '2026-09-10T00:00:00Z' }] })
 act(() => window.dispatchEvent(new CustomEvent(nativeActivityEvent, { detail: activity })))
 expect(hook.result.current.map(s => s.id)).toEqual(['current', 'other'])
 hook.unmount()
})
