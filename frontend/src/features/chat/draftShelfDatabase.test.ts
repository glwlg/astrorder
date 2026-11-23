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

it('uses lightweight meta probe to skip full downloads when empty or cached revision matches', async () => {
  let revision = 0
  let entries: Array<{ id: string; savedAt: string; draft: { text: string; attachments: Array<{ key: string; file: { name: string; type: string; lastModified: number; data: string } }> } }> = []
  let fullGets = 0
  let metaGets = 0
  const fetchMock = vi.fn(async (url: unknown, init?: RequestInit) => {
    const href = String(url)
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body))
      revision = body.revision + 1
      entries = body.entries
      return new Response(JSON.stringify({ revision, entries }), { status: 200 })
    }
    if (href.includes('meta=1')) {
      metaGets += 1
      return new Response(JSON.stringify({ revision, count: entries.length }), { status: 200 })
    }
    fullGets += 1
    return new Response(JSON.stringify({ revision, entries }), { status: 200 })
  })
  vi.stubGlobal('fetch', fetchMock)

  // Empty shelf: only meta=1 probe, zero full GETs
  expect(await draftShelfStorage.load('meta-scope')).toEqual([])
  expect(metaGets).toBe(1)
  expect(fullGets).toBe(0)

  // Remote revision advances with an attachment
  revision = 2
  entries = [{
    id: 'remote-1',
    savedAt: '2026-11-23T00:00:00Z',
    draft: {
      text: '大截图草稿',
      attachments: [{ key: 'img', file: { name: 'big.png', type: 'image/png', lastModified: 1, data: 'AAEC' } }],
    },
  }]
  const loaded = await draftShelfStorage.load('meta-scope')
  expect(loaded).toHaveLength(1)
  expect(loaded[0].draft.text).toBe('大截图草稿')
  expect(fullGets).toBe(1)

  // Repeat session switch with unchanged revision: served from revision cache, zero extra full GETs
  const cached = await draftShelfStorage.load('meta-scope')
  expect(cached).toHaveLength(1)
  expect(cached[0].draft.text).toBe('大截图草稿')
  expect(metaGets).toBe(3)
  expect(fullGets).toBe(1)

  // Revision bumps externally: triggers a fresh full GET
  revision = 3
  entries = [{ ...entries[0], draft: { ...entries[0].draft, text: '更新后的草稿' } }]
  const refreshed = await draftShelfStorage.load('meta-scope')
  expect(refreshed[0].draft.text).toBe('更新后的草稿')
  expect(fullGets).toBe(2)
})
