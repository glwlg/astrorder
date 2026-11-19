import { StrictMode } from 'react'
import { act, renderHook, waitFor } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { browserDesign, type BrowserDesignResponse } from './browserDesign'
import { useBrowserDesigner } from './useBrowserDesigner'

vi.mock('./browserDesign', async () => ({ ...await vi.importActual('./browserDesign'), browserDesign: vi.fn() }))

const response: BrowserDesignResponse = {
  snapshot: { url: 'http://localhost:3000', title: '页面', screenshot: 'eA==', mime_type: 'image/jpeg', revision: 1, captured_at: 1 },
  state: {
    document_id: 'document', url: 'http://localhost:3000', title: '页面', viewport: { width: 1280, height: 800 },
    changes: [], can_undo: false,
    selected: { id: 'button', tag: 'button', selector: '#button', text: '保存', classes: [], rect: { x: 1, y: 1, width: 30, height: 30 }, styles: {} as never, has_parent: true },
  },
}
const onSnapshot = vi.fn()

beforeEach(() => {
  vi.clearAllMocks()
  vi.mocked(browserDesign).mockResolvedValue(response)
})

it('initializes in StrictMode without keeping the editor busy', async () => {
  const { result } = renderHook(() => useBrowserDesigner('agent', 'session', 'target', onSnapshot), { wrapper: StrictMode })
  await waitFor(() => expect(result.current.state?.document_id).toBe('document'))
  expect(result.current.busy).toBe(false)
  expect(browserDesign).toHaveBeenCalledTimes(1)
})

it('coalesces rapid changes and flushes all styles before exporting', async () => {
  const { result } = renderHook(() => useBrowserDesigner('agent', 'session', 'target', onSnapshot))
  await waitFor(() => expect(result.current.state).not.toBeNull())
  act(() => {
    result.current.change('width', '120px')
    result.current.change('width', '140px')
    result.current.change('font-size', '18px')
  })
  expect(result.current.busy).toBe(true)
  await act(async () => { await result.current.action({ action: 'export' }) })
  const actions = vi.mocked(browserDesign).mock.calls.map(call => call[3])
  expect(actions.map(command => command.action)).toEqual(['state', 'apply', 'apply', 'export'])
  expect(actions[1]).toMatchObject({ element_id: 'button', document_id: 'document', property: 'width', value: '140px' })
  expect(result.current.busy).toBe(false)
})

it('keeps edit errors visible and stops export after a failed flush', async () => {
  const { result } = renderHook(() => useBrowserDesigner('agent', 'session', 'target', onSnapshot))
  await waitFor(() => expect(result.current.state).not.toBeNull())
  vi.mocked(browserDesign).mockRejectedValue(new Error('页面已变化'))
  act(() => { result.current.change('width', '120px') })
  let exported: BrowserDesignResponse | null | undefined
  await act(async () => { exported = await result.current.action({ action: 'export' }) })
  expect(exported).toBeNull()
  expect(result.current.error).toBe('页面已变化')
  expect(vi.mocked(browserDesign).mock.calls.at(-1)![3].action).toBe('apply')
  expect(result.current.busy).toBe(false)
})
