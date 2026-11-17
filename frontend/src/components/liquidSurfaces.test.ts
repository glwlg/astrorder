import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import { installLiquidSurfaces } from './liquidSurfaces'

const { liquidGL, destroy, captureSnapshot, renderer } = vi.hoisted(() => {
  const destroy = vi.fn()
  const captureSnapshot = vi.fn().mockResolvedValue(true)
  const renderer = { captureSnapshot, canvas: null as unknown as HTMLCanvasElement }
  return { destroy, captureSnapshot, renderer, liquidGL: vi.fn((_options: unknown) => ({ destroy, renderer })) }
})
vi.mock('liquid-gl', () => ({ default: liquidGL }))
let dispose: (() => void) | undefined

beforeEach(() => {
  vi.clearAllMocks()
  renderer.canvas = document.createElement('canvas')
  vi.spyOn(HTMLElement.prototype, 'getClientRects').mockReturnValue([{ width: 120, height: 40 }] as unknown as DOMRectList)
})
afterEach(() => {
  dispose?.()
  dispose = undefined
  document.body.replaceChildren()
  vi.restoreAllMocks()
  vi.useRealTimers()
})

it('decorates portalled menus without changing controls, focus, events or menu semantics', async () => {
  document.body.innerHTML = '<div class="mantine-Menu-dropdown" role="menu"><button role="menuitem">打开</button></div>'
  const button = document.querySelector('button')!
  const onClick = vi.fn()
  button.addEventListener('click', onClick)
  button.focus()
  dispose = installLiquidSurfaces()
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  expect(document.activeElement).toBe(button)
  expect(button.closest('[role="menu"]')).toBe(document.querySelector('.liquid-surface'))
  expect(document.querySelector('.liquid-surface-layer')).toHaveAttribute('aria-hidden', 'true')
  button.click()
  expect(onClick).toHaveBeenCalledOnce()
  dispose()
  dispose = undefined
  expect(document.querySelector('.liquid-surface-layer')).toBeNull()
  expect(document.querySelector('[data-liquid-ignore]')).toBeNull()
  expect(document.activeElement).toBe(button)
  expect(destroy).toHaveBeenCalledOnce()
})

it('releases deselected and removed surfaces and binds new menus only once', async () => {
  document.body.innerHTML = '<a data-liquid-surface="selection">会话</a>'
  dispose = installLiquidSurfaces()
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  document.querySelector('a')!.removeAttribute('data-liquid-surface')
  await waitFor(() => expect(destroy).toHaveBeenCalledOnce())
  const menu = document.createElement('div')
  menu.className = 'mantine-Popover-dropdown'
  document.body.append(menu)
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(2))
  menu.append(document.createElement('button'))
  await new Promise(resolve => setTimeout(resolve, 30))
  expect(liquidGL).toHaveBeenCalledTimes(2)
  menu.remove()
  await waitFor(() => expect(destroy).toHaveBeenCalledTimes(2))
})

it('keeps the material when a framework updates a surface class without registering another lens', async () => {
  document.body.innerHTML = '<div class="mantine-Menu-dropdown"></div>'
  dispose = installLiquidSurfaces()
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  document.querySelector('.mantine-Menu-dropdown')!.className = 'mantine-Menu-dropdown menu-open'
  await waitFor(() => expect(document.querySelector('.menu-open')).toHaveClass('liquid-surface'))
  expect(liquidGL).toHaveBeenCalledOnce()
})

it('does not bind existing composer menus twice, but supports explicit nested mention menus', async () => {
  document.body.innerHTML = '<div class="mantine-Popover-dropdown"><div class="glass-panel"><div class="mantine-Menu-dropdown"></div><div data-liquid-surface="menu" data-liquid-snapshot=".group"></div></div></div>'
  dispose = installLiquidSurfaces({ dark: true, reducedMotion: true })
  await waitFor(() => expect(liquidGL).toHaveBeenCalledOnce())
  expect(liquidGL).toHaveBeenCalledWith(expect.objectContaining({ snapshot: '.group', specular: false, refraction: 0, frost: 0 }))
})

it('refreshes shared renderers once for nested scrolls and cancels pending snapshots on disposal', async () => {
  document.body.innerHTML = '<div data-liquid-surface="control"></div><div data-liquid-surface="toolbar"></div>'
  dispose = installLiquidSurfaces()
  await waitFor(() => expect(liquidGL).toHaveBeenCalledTimes(2))
  await new Promise(resolve => setTimeout(resolve, 180))
  captureSnapshot.mockClear()
  vi.useFakeTimers()
  document.body.dispatchEvent(new Event('scroll'))
  document.body.dispatchEvent(new Event('scroll'))
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledOnce()
  document.body.dispatchEvent(new Event('scroll'))
  dispose()
  dispose = undefined
  await vi.advanceTimersByTimeAsync(160)
  expect(captureSnapshot).toHaveBeenCalledOnce()
})

it('preserves caller-owned ignore attributes and cancels initialization after immediate unmount', async () => {
  document.body.innerHTML = '<div data-liquid-surface="control" data-liquid-ignore></div>'
  dispose = installLiquidSurfaces()
  dispose()
  dispose = undefined
  await Promise.resolve()
  expect(liquidGL).not.toHaveBeenCalled()
  expect(document.querySelector('[data-liquid-ignore]')).not.toBeNull()
})
