import type { ReactNode } from 'react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api, type SessionModelBinding } from '../api/client'
import { useSessionModel } from './useSessionModel'
const session = { id: 'native', agent_id: 'a', title: 'Test', status: 'idle' as const, workspace: null, updated_at: '2026-09-08T00:00:00Z' }
afterEach(() => { cleanup(); vi.restoreAllMocks() })
function wrapper({ children }: { children: ReactNode }) { return <QueryClientProvider client={client}>{children}</QueryClientProvider> }
let client: QueryClient
it('a late model read cannot overwrite a native-confirmed model change', async () => {
  client = new QueryClient()
  let resolve!: (binding: SessionModelBinding) => void
  vi.spyOn(api, 'getSessionModel').mockImplementation(() => new Promise(done => { resolve = done }))
  vi.spyOn(api, 'setSessionModel').mockResolvedValue({ provider: 'p', model: 'new-model' })
  const hook = renderHook(() => useSessionModel(session), { wrapper })
  await waitFor(() => expect(api.getSessionModel).toHaveBeenCalled())
  await act(async () => { await hook.result.current.change('p', 'new-model') })
  await act(async () => { resolve({ provider: 'p', model: 'old-model' }); await Promise.resolve() })
  await waitFor(() => expect(hook.result.current.isFetching).toBe(false))
  expect(hook.result.current.label).toBe('p/new-model')
})
it('blocks read-only mutations and restores them after a fresh writable binding', async () => {
  client = new QueryClient()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'p', model: 'native', read_only: true })
  const setModel = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ provider: 'p', model: 'next' })
  const setEffort = vi.spyOn(api, 'setSessionReasoning').mockResolvedValue({ effort: 'high' })
  const hook = renderHook(() => useSessionModel(session), { wrapper })
  await waitFor(() => expect(hook.result.current.data?.read_only).toBe(true))
  await expect(hook.result.current.change('p', 'next')).rejects.toThrow('占用')
  await expect(hook.result.current.changeEffort('high')).rejects.toThrow('占用')
  expect(setModel).not.toHaveBeenCalled()
  expect(setEffort).not.toHaveBeenCalled()
  vi.mocked(api.getSessionModel).mockResolvedValue({ provider: 'p', model: 'native' })
  await act(async () => { await hook.result.current.refetch() })
  await waitFor(() => expect(hook.result.current.data?.read_only).toBeUndefined())
  await act(async () => { await hook.result.current.change('p', 'next'); await hook.result.current.changeEffort('high') })
  expect(setModel).toHaveBeenCalledTimes(1)
  expect(setEffort).toHaveBeenCalledTimes(1)
})
it('blocks fixed native model mutations without declaring the conversation read-only', async () => {
  client = new QueryClient()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'antigravity', model: 'native', model_read_only: true, detail: '固定模型' })
  const setModel = vi.spyOn(api, 'setSessionModel')
  const setEffort = vi.spyOn(api, 'setSessionReasoning')
  const hook = renderHook(() => useSessionModel(session), { wrapper })
  await waitFor(() => expect(hook.result.current.data?.model_read_only).toBe(true))
  expect(hook.result.current.data?.read_only).toBeUndefined()
  await expect(hook.result.current.change('antigravity', 'next')).rejects.toThrow('固定模型')
  await expect(hook.result.current.changeEffort('high')).rejects.toThrow('固定模型')
  expect(setModel).not.toHaveBeenCalled()
  expect(setEffort).not.toHaveBeenCalled()
})
it('reused native IDs on different sources never share model labels', async () => {
  client = new QueryClient()
  const pending: Record<string, (value: SessionModelBinding) => void> = {}
  vi.spyOn(api, 'getSessionModel').mockImplementation((_id, agent) => new Promise(done => { pending[agent] = done }))
  const hook = renderHook(({ agent }) => useSessionModel({ ...session, agent_id: agent }), { wrapper, initialProps: { agent: 'a' } })
  await waitFor(() => expect(pending.a).toBeDefined())
  hook.rerender({ agent: 'b' })
  await waitFor(() => expect(pending.b).toBeDefined())
  await act(async () => { pending.b({ provider: 'p', model: 'model-b' }) })
  await waitFor(() => expect(hook.result.current.label).toBe('p/model-b'))
  await act(async () => { pending.a({ provider: 'p', model: 'model-a' }) })
  expect(hook.result.current.label).toBe('p/model-b')
})
it('offers model selection when a successful native read omits the model', async () => {
  client = new QueryClient()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'antigravity', model: '', model_read_only: false, reasoning_read_only: true })
  const hook = renderHook(() => useSessionModel(session), { wrapper })
  await waitFor(() => expect(hook.result.current.isSuccess).toBe(true))
  expect(hook.result.current.label).toBe('选择模型')
})
it('allows model switching while unsupported reasoning remains disabled', async () => {
  client = new QueryClient()
  vi.spyOn(api, 'getSessionModel').mockResolvedValue({ provider: 'antigravity', model: 'native', reasoning_read_only: true } as SessionModelBinding)
  const setModel = vi.spyOn(api, 'setSessionModel').mockResolvedValue({ provider: 'antigravity', model: 'next' })
  const setEffort = vi.spyOn(api, 'setSessionReasoning')
  const hook = renderHook(() => useSessionModel(session), { wrapper })
  await waitFor(() => expect(hook.result.current.data?.model).toBe('native'))
  await act(async () => { await hook.result.current.change('antigravity', 'next') })
  await expect(hook.result.current.changeEffort('high')).rejects.toThrow('思考强度')
  expect(setModel).toHaveBeenCalledTimes(1)
  expect(setEffort).not.toHaveBeenCalled()
})
