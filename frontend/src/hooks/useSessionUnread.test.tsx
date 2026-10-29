import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { EventNotification } from '../domain/types'
import { useAstrorderStore } from '../state/store'
import { useSessionUnread } from './useSessionUnread'

const session = { id: 's', agent_id: 'a', title: '旧会话', workspace: '/repo', status: 'idle' as const, updated_at: '2026-10-01T00:00:00Z' }
const notice = (key: string, cursor: number): EventNotification => ({
  key, cursor, kind: 'task_completed', agent_id: 'a', session_id: 's',
  title: '会话已完成', message: '真实回复', created_at: '2026-10-01T00:00:00Z',
})
beforeEach(() => {
  localStorage.clear()
  useAstrorderStore.getState().resetRuntime()
  vi.spyOn(document, 'hasFocus').mockReturnValue(true)
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
})
afterEach(() => { cleanup(); vi.restoreAllMocks() })

it('never derives unread from recent metadata, status changes, or native activity timestamps', () => {
  const { result } = renderHook(() => useSessionUnread())
  act(() => {
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, agents: [], sessions: [session], cursor: 1 })
    useAstrorderStore.getState().applyEvent({ id: 'metadata', type: 'session.upsert', agent_id: 'a', session_id: 's', cursor: 2, data: { ...session, status: 'error', updated_at: '2099-01-01T00:00:00Z' } })
  })
  expect(result.current.isUnread('a::s')).toBe(false)
})
it('acknowledges a real result without opening a session and preserves it across refresh and replay', () => {
  act(() => useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, agents: [], sessions: [session], notifications: [notice('n1', 1)], cursor: 1 }))
  const first = renderHook(() => useSessionUnread())
  expect(first.result.current.isUnread('a::s')).toBe(true)
  act(() => first.result.current.markRead('a::s'))
  expect(first.result.current.isUnread('a::s')).toBe(false)
  first.unmount()
  useAstrorderStore.getState().resetRuntime()
  useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, agents: [], sessions: [session], notifications: [notice('n1', 1)], cursor: 1 })
  const next = renderHook(() => useSessionUnread())
  expect(next.result.current.isUnread('a::s')).toBe(false)
  act(() => {
    useAstrorderStore.getState().addNotification(notice('n2', 2))
    useAstrorderStore.getState().hydrateBootstrap({ protocol_version: 1, agents: [], sessions: [session], notifications: [notice('n1', 1)], cursor: 1 })
  })
  expect(next.result.current.isUnread('a::s')).toBe(true)
})
it('keeps reading new results while visible, but does not acknowledge a background tab', () => {
  const { result, rerender } = renderHook(({ active }) => useSessionUnread(active), { initialProps: { active: 'a::s' } })
  act(() => useAstrorderStore.getState().addNotification(notice('n1', 1)))
  expect(result.current.isUnread('a::s')).toBe(false)
  rerender({ active: '' })
  expect(result.current.isUnread('a::s')).toBe(false)
  rerender({ active: 'a::s' })
  vi.mocked(document.hasFocus).mockReturnValue(false)
  act(() => useAstrorderStore.getState().addNotification(notice('n2', 2)))
  expect(result.current.isUnread('a::s')).toBe(true)
  vi.mocked(document.hasFocus).mockReturnValue(true)
  act(() => window.dispatchEvent(new Event('focus')))
  expect(result.current.isUnread('a::s')).toBe(false)
})
it('shares acknowledgement with another mounted view without crossing session identities', () => {
  useAstrorderStore.getState().addNotification(notice('n1', 1))
  useAstrorderStore.getState().addNotification({ ...notice('other-agent', 2), agent_id: 'b' })
  const first = renderHook(() => useSessionUnread())
  const second = renderHook(() => useSessionUnread())
  act(() => first.result.current.markRead('a::s'))
  expect(second.result.current.isUnread('a::s')).toBe(false)
  expect(second.result.current.isUnread('b::s')).toBe(true)
})
