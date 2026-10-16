import { afterEach, expect, it, vi } from 'vitest'
import { notifications } from '@mantine/notifications'
import { downloadFile } from './fileTransfer'

vi.mock('@mantine/notifications', () => ({ notifications: { show: vi.fn(() => 'download'), update: vi.fn() } }))
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); vi.clearAllMocks(); delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop })
it('saves browser downloads from authenticated bytes', async () => {
 localStorage.setItem('astrorder:token', 'fixture-download')
 const fetch = vi.fn().mockResolvedValue(new Response(new Uint8Array([0,1,255])))
 vi.stubGlobal('fetch',fetch)
 const create = vi.fn().mockReturnValue('blob:download')
 vi.stubGlobal('URL',Object.assign(URL,{createObjectURL:create,revokeObjectURL:vi.fn()}))
 const click = vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(function(this: HTMLAnchorElement){expect(this.download).toBe('文件.bin');expect(this.href).toBe('blob:download')})
 await downloadFile('/api/v1/files/raw?path=%2Ftmp%2Ffile&connection_id=ssh-test','文件.bin')
 expect(new Headers(fetch.mock.calls[0][1].headers).get('Authorization')).toBe('Bearer fixture-download')
 expect(click).toHaveBeenCalledTimes(1)
 expect(new Uint8Array(await create.mock.calls[0][0].arrayBuffer())).toEqual(new Uint8Array([0,1,255]))
})
it('reports HTTP failure without starting a browser download',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({detail:'file missing'}),{status:404})))
 const click=vi.spyOn(HTMLAnchorElement.prototype,'click').mockImplementation(()=>{})
 await downloadFile('/api/v1/files/raw?path=missing','missing')
 expect(click).not.toHaveBeenCalled()
 expect(notifications.update).toHaveBeenCalledWith(expect.objectContaining({title:'下载文件失败',message:'file missing',loading:false}))
})
