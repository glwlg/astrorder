import { MantineProvider } from '@mantine/core'
import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { SidecarBlackboardPanel } from './SidecarBlackboardPanel'

vi.mock('./GroupBlackboardItem', () => ({
  GroupBlackboardItem: ({ itemKey }: { itemKey: string }) => <div data-testid={`bb-${itemKey}`}>{itemKey}</div>,
}))

describe('SidecarBlackboardPanel event-driven refresh', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(api, 'getBlackboard').mockResolvedValue({ namespace: 'session:1', items: { status: 'ok' }, count: 1 })
  })

  afterEach(() => {
    cleanup()
    vi.restoreAllMocks()
    vi.useRealTimers()
    Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
  })

  it('refreshes on matching blackboard-change events instead of 4-second polling', async () => {
    render(
      <MantineProvider>
        <SidecarBlackboardPanel namespace="session:1" />
      </MantineProvider>,
    )
    await act(async () => {
      await Promise.resolve()
    })
    expect(api.getBlackboard).toHaveBeenCalledTimes(1)

    // No 4-second polling storm over 20 seconds of idle time.
    await act(async () => {
      await vi.advanceTimersByTimeAsync(20_000)
    })
    expect(api.getBlackboard).toHaveBeenCalledTimes(1)

    // Unrelated namespace event is ignored.
    await act(async () => {
      window.dispatchEvent(new CustomEvent('astrorder:blackboard-change', { detail: { namespace: 'session:other' } }))
      await vi.advanceTimersByTimeAsync(300)
    })
    expect(api.getBlackboard).toHaveBeenCalledTimes(1)

    // Matching namespace bursts coalesce into a single debounced fetch.
    await act(async () => {
      window.dispatchEvent(new CustomEvent('astrorder:blackboard-change', { detail: { namespace: 'session:1' } }))
      window.dispatchEvent(new CustomEvent('astrorder:blackboard-change', { detail: { namespace: 'session:1' } }))
      await vi.advanceTimersByTimeAsync(200)
    })
    expect(api.getBlackboard).toHaveBeenCalledTimes(2)

    // Fallback poll pauses while hidden.
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(60_000)
    })
    expect(api.getBlackboard).toHaveBeenCalledTimes(2)
  })
})
