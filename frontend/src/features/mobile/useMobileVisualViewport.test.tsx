import { useRef } from 'react'
import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useMobileVisualViewport } from './useMobileVisualViewport'

function Harness() {
  const ref = useRef<HTMLDivElement>(null)
  useMobileVisualViewport(ref)
  return (
    <div ref={ref} data-testid="workspace" className="mobile-workspace">
      <textarea aria-label="输入" />
    </div>
  )
}

const originalVisualViewport = Object.getOwnPropertyDescriptor(window, 'visualViewport')
const originalInnerHeight = Object.getOwnPropertyDescriptor(window, 'innerHeight')

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  if (originalVisualViewport) Object.defineProperty(window, 'visualViewport', originalVisualViewport)
  else Reflect.deleteProperty(window, 'visualViewport')
  if (originalInnerHeight) Object.defineProperty(window, 'innerHeight', originalInnerHeight)
})

describe('useMobileVisualViewport', () => {
  it('syncs workspace height and offsetTop when keyboard opens and resets cleanly on dismiss', () => {
    const listeners = new Map<string, Set<EventListener>>()
    const vv = {
      height: 844,
      offsetTop: 0,
      addEventListener: (type: string, fn: EventListener) => {
        if (!listeners.has(type)) listeners.set(type, new Set())
        listeners.get(type)!.add(fn)
      },
      removeEventListener: (type: string, fn: EventListener) => {
        listeners.get(type)?.delete(fn)
      },
    }
    const emit = (type: string) => listeners.get(type)?.forEach(fn => fn(new Event(type)))

    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 844 })
    Object.defineProperty(window, 'visualViewport', { configurable: true, value: vv })
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation(cb => {
      cb(0)
      return 1
    })
    vi.spyOn(window, 'scrollTo').mockImplementation(() => {})

    render(<Harness />)
    const root = screen.getByTestId('workspace')
    const input = screen.getByLabelText('输入')

    expect(root.dataset.keyboardOpen).toBeUndefined()

    // Simulate iOS keyboard opening + visual viewport shifting
    input.focus()
    vv.height = 468
    vv.offsetTop = 24
    act(() => emit('resize'))

    expect(root.dataset.keyboardOpen).toBe('true')
    expect(root.style.getPropertyValue('--m-vv-height')).toBe('468px')
    expect(root.style.getPropertyValue('--m-vv-top')).toBe('24px')

    // Simulate keyboard dismissing back to full screen
    vv.height = 844
    vv.offsetTop = 0
    act(() => emit('resize'))

    expect(root.dataset.keyboardOpen).toBeUndefined()
    expect(root.style.getPropertyValue('--m-vv-height')).toBe('')
    expect(root.style.getPropertyValue('--m-vv-top')).toBe('')
  })
})
