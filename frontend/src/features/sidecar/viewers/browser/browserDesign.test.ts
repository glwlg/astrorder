import { beforeEach, expect, it, vi } from 'vitest'
import { addDesignToDraft, cssColorToHex, designTaskText, type BrowserDesignResponse } from './browserDesign'
import { browserEntryUrls, normalizeBrowserUrl } from './browserEntries'
import { useAstrorderStore } from '../../../../state/store'
import { draftStorage } from '../../../chat/draftStorage'

vi.mock('../../../chat/draftStorage', () => ({ draftStorage: { load: vi.fn(), save: vi.fn() } }))

const response: BrowserDesignResponse = {
  snapshot: { url: 'http://localhost:5173/settings', title: '设置', screenshot: 'aGVsbG8=', mime_type: 'image/jpeg', revision: 1, captured_at: 1 },
  state: {
    document_id: 'page', url: 'http://localhost:5173/settings', title: '设置', viewport: { width: 1280, height: 800 },
    selected: null, can_undo: true,
    changes: [{ element_id: 'one', selector: '#save', tag: 'button', text: '保存', classes: ['save-button'], properties: [{ property: 'font-size', before: '16px', after: '14px' }] }],
  },
}

beforeEach(() => {
  vi.clearAllMocks()
  useAstrorderStore.setState({ drafts: {} })
})

it('describes screenshot feedback in plain language without DOM or transport metadata', () => {
  const text = designTaskText(response.state, '保持按钮位置')
  expect(text).toBe('保持按钮位置\n\n“保存”：字号 16px → 14px')
  expect(text).not.toMatch(/#save|save-button|viewport|selector|astrorder:|localhost/)
  expect(designTaskText({ ...response.state, changes: [] }, '去掉红框里的箭头\n保持按钮位置')).toBe('去掉红框里的箭头\n保持按钮位置')
})

it('preserves text typed during storage loading, attachments and session references', async () => {
  let finish!: (value: undefined) => void
  vi.mocked(draftStorage.load).mockReturnValue(new Promise(resolve => { finish = resolve }))
  const operation = addDesignToDraft('agent', 'session', response, '')
  const file = new File(['original'], '原图.png', { type: 'image/png' })
  useAstrorderStore.setState({ drafts: { 'agent::session': { text: '请继续处理', attachments: [{ key: 'original', file }], sessionRefs: [] } } })
  finish(undefined)
  await operation
  const draft = useAstrorderStore.getState().drafts['agent::session']
  expect(draft.text.startsWith('请继续处理\n\n')).toBe(true)
  expect(draft.attachments).toHaveLength(2)
  expect(draft.attachments[0].file).toBe(file)
  expect(draft.attachments[1].file.type).toBe('image/jpeg')
  expect(draft.attachments[1].file.size).toBe(5)
  expect(draftStorage.save).toHaveBeenCalledWith('agent::session', draft)
})

it('normalizes local development addresses and offers bounded deduplicated candidates', () => {
  expect(normalizeBrowserUrl(' localhost:5173/settings ')).toBe('http://localhost:5173/settings')
  expect(normalizeBrowserUrl('[::1]:3000')).toBe('http://[::1]:3000')
  expect(normalizeBrowserUrl('example.com')).toBe('https://example.com')
  expect(browserEntryUrls(['打开 http://localhost:5173', '[页面](http://localhost:5173) http://user:password@example.com'])).toEqual(['http://localhost:5173/'])
  expect(browserEntryUrls([Array.from({ length: 20 }, (_, index) => `http://localhost:${3000 + index}`).join(' ')])).toHaveLength(6)
})

it('keeps transparent colors distinct from opaque ones', () => {
  expect(cssColorToHex('rgb(12, 34, 56)')).toBe('#0c2238')
  expect(cssColorToHex('rgba(12, 34, 56, 1)')).toBe('#0c2238')
  expect(cssColorToHex('rgba(0, 0, 0, 0)')).toBeNull()
})
