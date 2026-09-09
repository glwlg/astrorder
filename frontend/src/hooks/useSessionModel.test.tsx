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
