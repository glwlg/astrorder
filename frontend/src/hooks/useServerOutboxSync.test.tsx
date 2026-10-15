import { act, renderHook, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { api } from '../api/client'
import { useAstrorderStore } from '../state/store'
import { useServerOutboxSync } from './useServerOutboxSync'
const memory = vi.hoisted(() => ({ rows: [] as import('../features/mobile/mobileOutbox').OutboxEntry[] }))
vi.mock('../features/mobile/mobileOutboxStorage', () => ({ mobileOutboxStorage: { load: async () => memory.rows, save: async (rows: typeof memory.rows) => { memory.rows = rows } } }))
afterEach(() => { vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })
it('migrates another session on reconnect without mounting a chat', async () => {
 useAstrorderStore.getState().resetRuntime()
 memory.rows = [{payload:{id:'offline-other',agent_id:'a',session_id:'other',action:'send',text:'next',attachment_ids:[],target_id:null},files:[],attachments:[],state:'queued'}]
 const create = vi.spyOn(api,'createCommand').mockImplementation(async payload => ({...payload,state:'queued',attachments:[],created_at:'2026-10-15T00:00:00Z',error:null}))
 const view = renderHook(() => useServerOutboxSync(true))
 expect(create).not.toHaveBeenCalled()
 act(() => useAstrorderStore.getState().setConnection('connected'))
 await waitFor(() => expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({id:'offline-other',session_id:'other',action:'enqueue'})))
 await waitFor(() => expect(memory.rows).toEqual([]))
 view.unmount()
})
