import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import type { Task } from '../../domain/types'
import { useBackgroundProcesses } from './useBackgroundProcesses'

afterEach(() => vi.restoreAllMocks())
it('discards a previous session response after switching and clears on completion', async () => {
  let resolveOld!: (value: { items: Task[] }) => void
  const old = new Promise<{ items: Task[] }>(resolve => { resolveOld = resolve })
  const task = { id: 'new-process', kind: 'background', status: 'running' } as Task
  const read = vi.spyOn(api, 'getBackgroundProcesses').mockReturnValueOnce(old).mockResolvedValue({ items: [task] })
  const { result, rerender, unmount } = renderHook(({ id }) => useBackgroundProcesses({ id, agent_id: 'a' }, true), { initialProps: { id: 'old' } })
  rerender({ id: 'new' })
  await waitFor(() => expect(result.current.items).toEqual([task]))
  await act(async () => resolveOld({ items: [{ ...task, id: 'old-process' }] }))
  expect(result.current.items).toEqual([task])
  read.mockResolvedValue({ items: [] })
  await waitFor(() => expect(result.current.items).toEqual([]), { timeout: 4000 })
  expect(read).toHaveBeenLastCalledWith('new', 'a')
  unmount()
})
it('surfaces failed native reads instead of retaining a fake running state', async () => {
  vi.spyOn(api, 'getBackgroundProcesses').mockRejectedValue(new Error('gateway unavailable'))
  const { result, unmount } = renderHook(() => useBackgroundProcesses({ id: 's', agent_id: 'a' }, true))
  await waitFor(() => expect(result.current.error).toBe('gateway unavailable'))
  expect(result.current.items).toEqual([])
  unmount()
})
