import { MantineProvider } from '@mantine/core'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import type { ArtifactRef } from '../../../domain/artifact'
import { MermaidViewer } from './mermaid/MermaidViewer'

vi.mock('../../../components/renderMermaid', () => ({ renderMermaid: vi.fn(async () => ({ svg: '<svg></svg>' })) }))

const artifact: ArtifactRef = { id: 'diagram', sessionId: 'session', agentId: 'agent', kind: 'workspace_file', name: 'flow.mmd', path: '/repo/flow.mmd', readUrl: '/api/v1/files/raw?path=flow.mmd', mediaType: 'text/vnd.mermaid', writable: true }

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} })
  vi.stubGlobal('fetch', vi.fn(async () => new Response('graph TD; A-->B', { status: 200 })))
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.removeItem('astrorder:token')
})

it('keeps an inactive Mermaid draft without intercepting save until reopened', async () => {
  const save = vi.fn(async () => true)
  const viewer = (isActive: boolean) => <MantineProvider env="test"><MermaidViewer artifact={artifact} isActive={isActive} onSave={save} /></MantineProvider>
  const view = render(viewer(true))
  fireEvent.click(screen.getByRole('button', { name: '编辑代码' }))
  await waitFor(() => expect(screen.getByRole('textbox')).toHaveValue('graph TD; A-->B'))
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'graph TD; A-->C' } })
  view.rerender(viewer(false))
  const hiddenSave = new KeyboardEvent('keydown', { key: 's', ctrlKey: true, cancelable: true })
  window.dispatchEvent(hiddenSave)
  expect(hiddenSave.defaultPrevented).toBe(false)
  expect(save).not.toHaveBeenCalled()
  expect(screen.getByRole('textbox')).toHaveValue('graph TD; A-->C')
  view.rerender(viewer(true))
  fireEvent.keyDown(window, { key: 's', metaKey: true })
  await waitFor(() => expect(save).toHaveBeenCalledExactlyOnceWith('graph TD; A-->C'))
})

it('authenticates diagram reads without needing a credential in the file URL', async () => {
  localStorage.setItem('astrorder:token', 'inert-diagram-fixture')
  render(<MantineProvider env="test"><MermaidViewer artifact={artifact} /></MantineProvider>)
  await waitFor(() => expect(fetch).toHaveBeenCalledWith(artifact.readUrl, expect.objectContaining({ credentials: 'include', headers: expect.any(Headers) })))
  const [, init] = vi.mocked(fetch).mock.calls[0]
  expect(new Headers(init?.headers).get('Authorization')).toBe('Bearer inert-diagram-fixture')
})

it('supports wheel zoom and drag pan directly inside Sidecar MermaidViewer', async () => {
  render(<MantineProvider env="test"><MermaidViewer artifact={artifact} /></MantineProvider>)
  const canvas = await screen.findByLabelText('Mermaid 缩放画布')

  fireEvent.wheel(canvas, { deltaY: -120, clientX: 150, clientY: 150 })
  expect(Number(canvas.getAttribute('data-zoom-scale'))).toBeGreaterThan(1)

  fireEvent.pointerDown(canvas, { pointerId: 1, pointerType: 'mouse', button: 0, clientX: 80, clientY: 80 })
  fireEvent.pointerMove(canvas, { pointerId: 1, pointerType: 'mouse', clientX: 130, clientY: 120 })
  fireEvent.pointerUp(canvas, { pointerId: 1, pointerType: 'mouse', clientX: 130, clientY: 120 })
  expect(Number(canvas.getAttribute('data-pan-x'))).not.toBe(0)
  expect(Number(canvas.getAttribute('data-pan-y'))).not.toBe(0)
})
