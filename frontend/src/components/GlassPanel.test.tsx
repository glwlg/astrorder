import { MantineProvider } from '@mantine/core'
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { createPortal } from 'react-dom'
import { ComposerGlassMenu, ComposerGlassSurface, GlassButtonLens, GlassPanel } from './GlassPanel'

const { liquidGL, destroy, captureSnapshot, renderer } = vi.hoisted(() => {
  const destroy = vi.fn()
  const captureSnapshot = vi.fn().mockResolvedValue(true)
  const renderer = { captureSnapshot, canvas: null as unknown as HTMLCanvasElement }
  return { destroy, captureSnapshot, renderer, liquidGL: vi.fn((_options: unknown) => ({ destroy, renderer })) }
})
vi.mock('liquid-gl', () => ({ default: liquidGL }))

beforeEach(() => {
  liquidGL.mockClear()
  destroy.mockClear()
  captureSnapshot.mockClear()
  renderer.canvas = document.createElement('canvas')
  const backdrop = document.createElement('div')
  backdrop.className = 'chat-workbench'
  document.body.append(backdrop)
})
afterEach(() => {
  cleanup()
  document.querySelector('.chat-workbench')?.remove()
  vi.useRealTimers()
})

it('refreshes nested scrolls and streamed text without recapturing its own UI or leaving pending work', async () => {
  const view = render(<MantineProvider><GlassPanel active snapshot=".chat-workbench">变更</GlassPanel></MantineProvider>)
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  vi.useFakeTimers()
  const backdrop = document.querySelector('.chat-workbench')!
  backdrop.dispatchEvent(new Event('scroll'))
  backdrop.dispatchEvent(new Event('scroll'))
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledOnce()
  backdrop.textContent = 'new streamed message'
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledTimes(2)
  view.container.querySelector('.glass-panel-content')!.textContent = '刷新中'
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledTimes(2)
  backdrop.dispatchEvent(new Event('scroll'))
  view.unmount()
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledTimes(2)
})

it('refracts the actual workbench, excludes its own controls, and releases the lens when hidden', async () => {
  const view = render(<MantineProvider><GlassPanel active snapshot=".chat-workbench"><button>会话操作</button></GlassPanel></MantineProvider>)
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(1))
  const options = liquidGL.mock.calls[0][0] as unknown as { snapshot: string; content: boolean }
  expect(options.snapshot).toBe('.chat-workbench')
  expect(view.container.querySelector('.glass-panel')).toHaveAttribute('data-liquid-ignore')
  expect(view.container.querySelector('.glass-panel-backdrop')).toBeNull()
  expect(options.content).toBe(false)

  view.rerender(<MantineProvider><GlassPanel active={false} snapshot=".chat-workbench"><button>会话操作</button></GlassPanel></MantineProvider>)
  expect(destroy).toHaveBeenCalledTimes(1)
  expect(screen.getByRole('button', { name: '会话操作' })).toBeVisible()
  expect(view.container.querySelector('.glass-panel')).not.toHaveAttribute('data-material-status')
  view.unmount()
  expect(destroy).toHaveBeenCalledTimes(1)
})

it('does not initialise a hidden panel, and destroys each instance on unmount', async () => {
  const view = render(<MantineProvider><GlassPanel active={false} snapshot=".chat-workbench">文件变更</GlassPanel></MantineProvider>)
  expect(liquidGL).not.toHaveBeenCalled()
  view.rerender(<MantineProvider><GlassPanel active snapshot=".chat-workbench">文件变更</GlassPanel></MantineProvider>)
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(1))
  view.unmount()
  expect(destroy).toHaveBeenCalledTimes(1)
})

it('binds controls that appear after loading, refreshes their shared renderer once, and releases removed controls', async () => {
  const content = (late: boolean) => <MantineProvider>
    <ComposerGlassSurface active>
      <button>附件<GlassButtonLens /></button>
      {late && <button>模型<GlassButtonLens /></button>}
    </ComposerGlassSurface>
  </MantineProvider>
  const view = render(content(false))
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(2))
  view.rerender(content(true))
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(3))
  vi.useFakeTimers()
  document.querySelector('.chat-workbench')!.dispatchEvent(new Event('scroll'))
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledOnce()
  view.rerender(content(false))
  await vi.advanceTimersByTimeAsync(0)
  expect(destroy).toHaveBeenCalledTimes(1)
  view.unmount()
  expect(destroy).toHaveBeenCalledTimes(3)
})

it('passes the material scope through portals and releases dropdown lenses immediately when closed', async () => {
  const content = (opened: boolean, enabled = true) => <MantineProvider>
    <ComposerGlassSurface active={enabled}>
      {createPortal(<ComposerGlassMenu active={opened}><button>切换模型</button></ComposerGlassMenu>, document.body)}
    </ComposerGlassSurface>
  </MantineProvider>
  const view = render(content(false))
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  view.rerender(content(true))
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(2))
  view.rerender(content(false))
  expect(destroy).toHaveBeenCalledOnce()
  expect(screen.getByRole('button', { name: '切换模型' })).toBeVisible()
  view.unmount()
  expect(destroy).toHaveBeenCalledTimes(2)
  liquidGL.mockClear()
  render(content(true, false))
  expect(liquidGL).not.toHaveBeenCalled()
})

it('uses the group backdrop for the composer and its portalled menus', async () => {
  const group = document.createElement('div')
  group.className = 'bot-group-chat-page'
  document.body.append(group)
  const view = render(<MantineProvider>
    <ComposerGlassSurface active snapshot=".bot-group-chat-page">
      <button>发送<GlassButtonLens /></button>
      {createPortal(<ComposerGlassMenu active>群成员</ComposerGlassMenu>, document.body)}
    </ComposerGlassSurface>
  </MantineProvider>)
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(3))
  for (const [options] of liquidGL.mock.calls) expect(options).toMatchObject({ snapshot: '.bot-group-chat-page' })
  view.unmount()
  group.remove()
})
