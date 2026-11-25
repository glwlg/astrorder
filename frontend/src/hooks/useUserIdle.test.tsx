import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import * as client from '../api/client'
import type { Session } from '../domain/types'
import { useSessionResources } from './useAstrorderData'
import { useGitStatus } from './useGitStatus'
import { useSessionSwarm } from './useSessionSwarm'
import {
  USER_IDLE_TIMEOUT_MS,
  isUserIdle,
  resetUserIdleForTest,
  subscribeUserIdle,
  useUserIdle,
} from './useUserIdle'

afterEach(() => {
  cleanup()
  resetUserIdleForTest()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

describe('useUserIdle', () => {
  it('transitions to idle after 15 minutes of inactivity and wakes immediately on interaction', () => {
    vi.useFakeTimers()
    const transitions: boolean[] = []
    const unsubscribe = subscribeUserIdle((idle) => transitions.push(idle))
    const { result } = renderHook(() => useUserIdle())

    expect(result.current).toBe(false)
    expect(isUserIdle()).toBe(false)

    // Repeated interactions while active should not emit spurious state transitions.
    act(() => {
      vi.advanceTimersByTime(10 * 60_000)
      window.dispatchEvent(new Event('keydown'))
      vi.advanceTimersByTime(10 * 60_000)
    })
    expect(result.current).toBe(false)
    expect(transitions).toEqual([])

    // Crossing the full inactivity threshold without events marks the user idle.
    act(() => {
      vi.advanceTimersByTime(USER_IDLE_TIMEOUT_MS - 10 * 60_000)
    })
    expect(result.current).toBe(true)
    expect(isUserIdle()).toBe(true)
    expect(transitions).toEqual([true])

    // Any interaction (including pointermove) wakes immediately.
    act(() => {
      window.dispatchEvent(new Event('pointermove'))
    })
    expect(result.current).toBe(false)
    expect(isUserIdle()).toBe(false)
    expect(transitions).toEqual([true, false])

    unsubscribe()
  })

  it('throttles pointermove while active yet resets the countdown once past the throttle window', () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useUserIdle())

    act(() => {
      vi.advanceTimersByTime(10_000)
      // Within 30s throttle window: ignored so high-frequency mouse motion stays zero-cost.
      window.dispatchEvent(new Event('pointermove'))
      vi.advanceTimersByTime(USER_IDLE_TIMEOUT_MS - 10_000)
    })
    expect(result.current).toBe(true)

    // Wake via pointerdown, then move pointer after 1 minute to extend active session.
    act(() => {
      window.dispatchEvent(new Event('pointerdown'))
      vi.advanceTimersByTime(60_000)
      window.dispatchEvent(new Event('pointermove'))
      vi.advanceTimersByTime(USER_IDLE_TIMEOUT_MS - 30_000)
    })
    expect(result.current).toBe(false)

    act(() => {
      vi.advanceTimersByTime(30_000)
    })
    expect(result.current).toBe(true)
  })

  it('treats hidden document visibility as idle immediately and resets timer on visible', () => {
    vi.useFakeTimers()
    let hidden = false
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const { result } = renderHook(() => useUserIdle())
    expect(result.current).toBe(false)

    act(() => {
      hidden = true
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(result.current).toBe(true)

    act(() => {
      vi.advanceTimersByTime(60 * 60_000)
      hidden = false
      document.dispatchEvent(new Event('visibilitychange'))
    })
    expect(result.current).toBe(false)

    act(() => {
      vi.advanceTimersByTime(USER_IDLE_TIMEOUT_MS)
    })
    expect(result.current).toBe(true)
  })

  it('pauses git status, swarm, and view-lease polling overnight and resumes cleanly on wake', async () => {
    vi.useFakeTimers()
    const session: Session = {
      id: 'overnight-s1',
      agent_id: 'agy-1',
      title: 'Overnight',
      status: 'running',
      workspace: '/repo',
      updated_at: '2026-11-25T00:00:00Z',
    }
    const gitReq = vi.spyOn(client, 'request').mockResolvedValue({
      branch: 'main',
      branches: ['main'],
      changed_files: 0,
      insertions: 0,
      deletions: 0,
      files: [],
    })
    const swarmReq = vi.spyOn(client.api, 'getSessionSwarm').mockResolvedValue({
      session_id: session.id,
      active: true,
      members: [],
    } as unknown as Awaited<ReturnType<typeof client.api.getSessionSwarm>>)
    const viewLeaseReq = vi.spyOn(client.api, 'renewSessionView').mockResolvedValue({ ok: true })
    vi.spyOn(client.api, 'syncSession').mockResolvedValue(session)
    vi.spyOn(client.api, 'getMessages').mockResolvedValue({ items: [], next_cursor: null })
    vi.spyOn(client.api, 'getCommands').mockResolvedValue({ items: [] })
    vi.spyOn(client.api, 'getTasks').mockResolvedValue({ items: [] })

    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    const wrapper = ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    )

    renderHook(() => {
      useGitStatus(session)
      useSessionSwarm(session, { kind: 'antigravity' }, false)
      useSessionResources(session, true, true)
    }, { wrapper })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(gitReq).toHaveBeenCalledTimes(1)
    expect(swarmReq).toHaveBeenCalledTimes(1)
    expect(viewLeaseReq).toHaveBeenCalledTimes(1)

    // Cross the 15-minute idle threshold, then record call counts at idle entry.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(USER_IDLE_TIMEOUT_MS + 1000)
    })
    expect(isUserIdle()).toBe(true)
    const gitAtIdle = gitReq.mock.calls.length
    const swarmAtIdle = swarmReq.mock.calls.length
    const leaseAtIdle = viewLeaseReq.mock.calls.length

    // Advance 6 unattended overnight hours: zero additional polls for all three endpoints.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(6 * 60 * 60_000)
    })
    expect(gitReq).toHaveBeenCalledTimes(gitAtIdle)
    expect(swarmReq).toHaveBeenCalledTimes(swarmAtIdle)
    expect(viewLeaseReq).toHaveBeenCalledTimes(leaseAtIdle)

    // User returns: view-lease renews immediately, and swarm/git intervals resume cleanly.
    await act(async () => {
      window.dispatchEvent(new Event('pointerdown'))
      await vi.advanceTimersByTimeAsync(0)
    })
    expect(isUserIdle()).toBe(false)
    expect(viewLeaseReq).toHaveBeenCalledTimes(leaseAtIdle + 1)

    await act(async () => {
      await vi.advanceTimersByTimeAsync(95_000)
    })
    expect(gitReq.mock.calls.length).toBeGreaterThan(gitAtIdle)
    expect(swarmReq.mock.calls.length).toBeGreaterThan(swarmAtIdle)
    queryClient.clear()
  })
})
