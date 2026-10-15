import { beforeEach, expect, it, vi } from 'vitest'
import { api } from '../../api/client'
import { useAstrorderStore } from '../../state/store'
import { ServerOutbox, sharedServerOutbox } from './serverOutbox'

beforeEach(() => { vi.restoreAllMocks(); useAstrorderStore.getState().resetRuntime() })
it('shares local staging across pages without overwriting another session', async () => {
 let rows: import('./mobileOutbox').OutboxEntry[] = []
 const storage = { load: async () => rows, save: async (next: typeof rows) => { rows = next } }
 const first = sharedServerOutbox(storage), second = sharedServerOutbox(storage)
 expect(first).toBe(second)
 await Promise.all([first.load(), second.load()])
 await Promise.all(['one','two'].map((id, i) => (i ? second : first).enqueue({id, agent_id:'a', session_id:id, action:'enqueue', text:id, attachment_ids:[], target_id:null}, [])))
 expect(rows.map(row => row.payload.id)).toEqual(['one','two'])
})
it('uploads attachments and persists queue immediately; another client sees and cancels it', async () => {
 const attachment = { id: 'blob', name: 'photo.png', media_type: 'image/png', url: '/blob' }
 const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValue(attachment)
 useAstrorderStore.getState().setConnection('connected')
 const create = vi.spyOn(api, 'createCommand').mockImplementation(async p => ({ ...p, state: 'queued', attachments: [attachment], created_at: '2026-10-15T00:00:00Z', error: null }))
 const storage = { load: async () => [], save: async () => {} }
 const desktop = new ServerOutbox(storage)
 const mobile = new ServerOutbox(storage)
 const unsubscribe = mobile.subscribe(() => {})
 await desktop.load(); await mobile.load()
 await desktop.enqueue({ id: 'queue', agent_id: 'a', session_id: 's', action: 'send', text: 'next', attachment_ids: [], target_id: null }, [new File(['abc'], 'photo.png', { type: 'image/png' })])
 expect(upload).toHaveBeenCalledOnce()
 expect(create).toHaveBeenCalledWith(expect.objectContaining({ action: 'enqueue', attachment_ids: ['blob'] }))
 expect(mobile.snapshot()[0]).toMatchObject({ state: 'queued', attachments: [attachment] })
 const cancel = vi.spyOn(api, 'deleteQueuedCommand').mockImplementation(async () => ({ ...create.mock.results[0].value && await create.mock.results[0].value, state: 'cancelled' }))
 await mobile.remove('a', 's', 'queue')
 expect(cancel).toHaveBeenCalledWith('queue', 'a', 's')
 expect(desktop.snapshot()).toEqual([])
 unsubscribe()
})
it('retains uploaded attachments and the command ID when queue acknowledgement is lost', async () => {
 useAstrorderStore.getState().setConnection('connected')
 let rows: import('./mobileOutbox').OutboxEntry[] = []
 const storage = { load: async () => rows, save: async (next: typeof rows) => { rows = next } }
 const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValue({ id: 'blob', name: 'photo.png', media_type: 'image/png', url: '/blob' })
 const create = vi.spyOn(api, 'createCommand').mockRejectedValueOnce(new Error('response lost')).mockImplementation(async p => ({ ...p, state: 'queued', attachments: [], created_at: '2026-10-15T00:00:00Z', error: null }))
 const outbox = new ServerOutbox(storage)
 await outbox.load()
 await outbox.enqueue({ id: 'same', agent_id: 'a', session_id: 's', action: 'send', text: 'next', attachment_ids: [], target_id: null }, [new File(['png'], 'photo.png', { type: 'image/png' })])
 expect(rows[0]).toMatchObject({ state: 'unknown', payload: { id: 'same', action: 'enqueue', attachment_ids: ['blob'] } })
 await outbox.migrate()
 expect(upload).toHaveBeenCalledOnce()
 expect(create.mock.calls[0][0]).toEqual(create.mock.calls[1][0])
 expect(rows).toEqual([])
})
it('keeps a partially uploaded queue item and resumes its remaining file', async () => {
 useAstrorderStore.getState().setConnection('connected')
 let rows: import('./mobileOutbox').OutboxEntry[] = []
 const storage = { load: async () => rows, save: async (next: typeof rows) => { rows = next } }
 const photo = { id: 'first', name: 'one.png', media_type: 'image/png', url: '/first' }
 const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValueOnce(photo).mockRejectedValueOnce(new Error('upload interrupted')).mockResolvedValueOnce({...photo, id:'second'})
 const create = vi.spyOn(api, 'createCommand').mockImplementation(async p => ({ ...p, state: 'queued', attachments: [], created_at: '2026-10-15T00:00:00Z', error: null }))
 const outbox = new ServerOutbox(storage)
 await expect(outbox.enqueue({id:'partial',agent_id:'a',session_id:'s',action:'enqueue',text:'photos',attachment_ids:[],target_id:null},[new File(['one'],'one.png'),new File(['two'],'two.png')])).resolves.toBeUndefined()
 expect(rows[0]).toMatchObject({state:'queued',error:'upload interrupted',payload:{attachment_ids:['first']}})
 expect(rows[0].files.map(file=>file.name)).toEqual(['two.png'])
 const restored = new ServerOutbox(storage)
 await restored.load(); await restored.migrate()
 expect(upload).toHaveBeenCalledTimes(3)
 expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({id:'partial',attachment_ids:['first','second']}))
 expect(rows).toEqual([])
})
it('keeps offline files locally, then migrates every session with the same command ID', async () => {
 let rows: import('./mobileOutbox').OutboxEntry[] = []
 const storage = { load: async () => rows, save: async (next: typeof rows) => { rows = next } }
 const file = new File(['png'], 'photo.png', { type: 'image/png' })
 const upload = vi.spyOn(api, 'uploadAttachment').mockResolvedValue({ id: 'blob', name: 'photo.png', media_type: 'image/png', url: '/blob' })
 const create = vi.spyOn(api, 'createCommand').mockImplementation(async p => ({ ...p, state: 'queued', attachments: [], created_at: '2026-10-15T00:00:00Z', error: null }))
 const outbox = new ServerOutbox(storage)
 await outbox.load()
 await outbox.enqueue({ id: 'offline', agent_id: 'a', session_id: 'other', action: 'send', text: 'offline', attachment_ids: [], target_id: null }, [file])
 expect(create).not.toHaveBeenCalled()
 expect(rows[0].files).toEqual([file])
 useAstrorderStore.getState().setConnection('connected')
 await outbox.migrate()
 expect(upload).toHaveBeenCalledOnce()
 expect(create).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ id: 'offline', action: 'enqueue', session_id: 'other', attachment_ids: ['blob'] }))
 expect(rows).toEqual([])
})
