import { afterEach, expect, it, vi } from 'vitest'
import { draftShelfStorage } from './draftStorage'

afterEach(() => vi.unstubAllGlobals())
it('stores image bytes remotely and restores them in another client load', async () => {
  let stored: unknown = { revision: 0, entries: [] }
  const fetch = vi.fn(async (_url: unknown, init?: RequestInit) => {
    if (init?.method === 'PUT') { const body = JSON.parse(String(init.body)); stored = { ...body, revision: body.revision + 1 } }
    return new Response(JSON.stringify(stored), { status: 200 })
  })
  vi.stubGlobal('fetch', fetch)
  await draftShelfStorage.load('remote-test')
  const file = new File([new Uint8Array([0, 1, 255])], 'shot.png', { type: 'image/png', lastModified: 123 })
  await draftShelfStorage.save('remote-test', [{ id: 'one', savedAt: new Date().toISOString(), draft: { text: '跨设备截图', attachments: [{ key: 'image', file }] } }])
  expect(fetch.mock.calls.some(([, init]) => init?.method === 'PUT')).toBe(true)
  const [restored] = await draftShelfStorage.load('remote-test')
  expect(restored.draft.text).toBe('跨设备截图')
  expect(restored.draft.attachments[0].file.name).toBe('shot.png')
  expect(Array.from(new Uint8Array(await restored.draft.attachments[0].file.arrayBuffer()))).toEqual([0, 1, 255])
})
