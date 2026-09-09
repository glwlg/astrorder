import { expect, it } from 'vitest'
import type { Session } from '../domain/types'
import { adjacentOpenSession, isSessionOpen } from './sessionVisibility'
import { buildProjectGroups } from './sessionRailModel'
const session = (id: string, is_open: boolean): Session => ({ id, agent_id: 'a', title: id, workspace: null, status: 'idle', updated_at: new Date().toISOString(), is_open })
it('swipes only through native-open sessions including idle open sessions', () => {
 const rows = [session('open-a', true), session('closed', false), session('open-b', true)]
 expect(isSessionOpen(rows[0])).toBe(true)
 expect(adjacentOpenSession(rows, rows[0], 1)?.id).toBe('open-b')
 expect(adjacentOpenSession(rows, rows[2], -1)?.id).toBe('open-a')
 expect(adjacentOpenSession(rows, rows[1], 1)?.id).toBe('open-b')
 expect(adjacentOpenSession(rows, rows[2], 1)).toBeUndefined()
})
it('24-hour filter excludes older sessions while keeping recent idle ones', () => {
 const rows = [session('recent', false), { ...session('old', true), updated_at: new Date(Date.now() - 25 * 3600000).toISOString() }]
 expect(buildProjectGroups(rows, {}, [], '', 'recent').flatMap(g => g.sessions).map(s => s.id)).toEqual(['recent'])
})
