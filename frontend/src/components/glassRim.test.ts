import { afterEach, expect, it, vi } from 'vitest'
import { waitFor } from '@testing-library/react'
import { createGlassRim } from './glassRim'

afterEach(() => {
  document.body.replaceChildren()
  vi.unstubAllGlobals()
})

function maskRects(canvas: HTMLCanvasElement) {
  const encoded = canvas.style.maskImage.match(/data:image\/svg\+xml,([^"]+)/)![1]
  return [...new DOMParser().parseFromString(decodeURIComponent(encoded), 'image/svg+xml').querySelectorAll('rect')]
}

it('masks only decorative rims, including controls sharing the canvas, and follows canvas scroll offsets', async () => {
  const canvas = document.createElement('canvas')
  const panel = document.createElement('div')
  const button = document.createElement('button')
  panel.textContent = '文件变更'
  panel.style.borderRadius = '18px'
  button.style.borderRadius = '7px'
  document.body.append(canvas, panel, button)
  let origin = 50
  vi.spyOn(canvas, 'getBoundingClientRect').mockImplementation(() => new DOMRect(origin, 20, 900, 600))
  vi.spyOn(panel, 'getBoundingClientRect').mockReturnValue(new DOMRect(80, 40, 360, 500))
  vi.spyOn(button, 'getBoundingClientRect').mockReturnValue(new DOMRect(120, 480, 32, 28))
  const rim = createGlassRim(canvas)
  rim.track(panel, 6)
  rim.track(button, 2)
  const [outer, control] = maskRects(canvas)
  expect(outer.getAttribute('fill')).toBe('none')
  expect(outer.getAttribute('x')).toBe('33')
  expect(outer.getAttribute('width')).toBe('354')
  expect(outer.getAttribute('stroke-width')).toBe('6')
  expect(control.getAttribute('x')).toBe('71')
  expect(control.getAttribute('width')).toBe('30')
  expect(control.getAttribute('stroke-width')).toBe('2')
  expect(panel.style.maskImage).toBe('')
  expect(button.style.maskImage).toBe('')

  origin = 10
  canvas.style.left = '-40px'
  await waitFor(() => expect(maskRects(canvas)[0].getAttribute('x')).toBe('73'))
  rim.untrack(button)
  expect(maskRects(canvas)).toHaveLength(1)
  rim.destroy()
  expect(canvas.style.maskImage).toBe('')
})

it('rebuilds the rim on resize, skips hidden targets, and disconnects observers on release', () => {
  const observe = vi.fn(), unobserve = vi.fn(), disconnect = vi.fn()
  let resize!: () => void
  vi.stubGlobal('ResizeObserver', class {
    constructor(callback: () => void) { resize = callback }
    observe = observe
    unobserve = unobserve
    disconnect = disconnect
  })
  const canvas = document.createElement('canvas')
  canvas.style.maskImage = 'none'
  const panel = document.createElement('div')
  panel.style.borderRadius = '18px'
  vi.spyOn(canvas, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 0, 900, 600))
  let width = 360
  vi.spyOn(panel, 'getBoundingClientRect').mockImplementation(() => new DOMRect(0, 0, width, 500))
  const rim = createGlassRim(canvas)
  expect(canvas.style.maskImage).toBe('linear-gradient(transparent, transparent)')
  rim.track(panel, 6)
  width = 240
  resize()
  expect(maskRects(canvas)[0].getAttribute('width')).toBe('234')
  width = 0
  resize()
  expect(canvas.style.maskImage).toBe('linear-gradient(transparent, transparent)')
  rim.untrack(panel)
  expect(unobserve).toHaveBeenCalledWith(panel)
  rim.destroy()
  expect(disconnect).toHaveBeenCalledOnce()
  expect(canvas.style.maskImage).toBe('none')
  expect(canvas.style.maskSize).toBe('')
})
