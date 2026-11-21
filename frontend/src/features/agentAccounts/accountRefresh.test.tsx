import { act, renderHook } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { useRefreshSettings, useScheduledRefresh } from './accountRefresh'

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); localStorage.clear() })

it('keeps both intervals off by default and remembers them per platform', () => {
  const first = renderHook(() => useRefreshSettings('antigravity'))
  expect(first.result.current[0]).toEqual({ all: 0, current: 0 })
  act(() => first.result.current[1]({ all: 10, current: 1 }))
  first.unmount()
  expect(renderHook(() => useRefreshSettings('antigravity')).result.current[0]).toEqual({ all: 10, current: 1 })
  expect(renderHook(() => useRefreshSettings('codex')).result.current[0]).toEqual({ all: 0, current: 0 })
})

it('waits a full interval, combines overlapping deadlines and pauses while hidden or unmounted', () => {
  vi.useFakeTimers()
  const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
  const refresh = vi.fn()
  const timer = renderHook(({ all, current }) => useScheduledRefresh('dev', { all, current }, refresh), { initialProps: { all: 0, current: 0 } })
  act(() => vi.advanceTimersByTime(600_000))
  expect(refresh).not.toHaveBeenCalled()
  timer.rerender({ all: 5, current: 1 })
  act(() => vi.advanceTimersByTime(300_000))
  expect(refresh.mock.calls.flat()).toEqual(['current', 'current', 'current', 'current', 'all'])
  hidden.mockReturnValue(true)
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  act(() => vi.advanceTimersByTime(300_000))
  expect(refresh).toHaveBeenCalledTimes(5)
  hidden.mockReturnValue(false)
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  expect(refresh.mock.calls.flat()).toEqual(['current', 'current', 'current', 'current', 'all', 'all'])
  act(() => vi.advanceTimersByTime(60_000))
  expect(refresh).toHaveBeenCalledTimes(7)
  timer.unmount()
  act(() => vi.advanceTimersByTime(300_000))
  expect(refresh).toHaveBeenCalledTimes(7)
})
