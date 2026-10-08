import { act, cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AstrorderLoader, SessionActivityBorder } from './AnimatedStatus'

vi.mock('motion/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('motion/react')>()
  const { createElement } = await import('react')
  const element = (tag: 'g' | 'rect' | 'path') => ({ animate, initial: _initial, exit: _exit, transition, children, ...props }: Record<string, unknown>) => createElement(tag, {
    ...props,
    'data-animate': JSON.stringify(animate),
    'data-repeat': transition && typeof transition === 'object' && 'repeat' in transition ? String(transition.repeat) : undefined,
  }, children as React.ReactNode)
  return { ...actual, AnimatePresence: ({ children }: { children: React.ReactNode }) => children, motion: { g: element('g'), rect: element('rect'), path: element('path') } }
})

afterEach(() => { cleanup(); vi.restoreAllMocks() })

function motionPreference(initial: boolean) {
  let matches = initial
  const listeners = new Set<(event: MediaQueryListEvent) => void>()
  const media = {
    get matches() { return matches },
    media: '(prefers-reduced-motion: reduce)',
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
    addListener: (listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeListener: (listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
  }
  vi.spyOn(window, 'matchMedia').mockReturnValue(media as unknown as MediaQueryList)
  return {
    change(value: boolean) { matches = value; act(() => { for (const listener of listeners) listener({ matches } as MediaQueryListEvent) }) },
    listenerCount: () => listeners.size,
  }
}

describe('SessionActivityBorder motion preference', () => {
  it('uses the shared CSS star border without a rotating SVG in either preference', () => {
    const { change } = motionPreference(true)
    const { container } = render(<SessionActivityBorder status="running" />)
    const border = container.querySelector('.session-star-border')!
    expect(border).toHaveAttribute('aria-hidden', 'true')
    expect(border.querySelectorAll('.session-star-glint')).toHaveLength(2)
    expect(container.querySelector('svg')).toBeNull()
    change(false)
    expect(container.querySelector('.session-star-border')).toBe(border)
  })

  it('removes the running effect immediately when the session becomes idle', () => {
    motionPreference(false)
    const { container, rerender } = render(<SessionActivityBorder status="running" />)
    expect(container.querySelector('.session-star-border')).not.toBeNull()
    rerender(<SessionActivityBorder status="idle" />)
    expect(container).toBeEmptyDOMElement()
  })

  it('resumes the loader without remounting it', () => {
    const { change } = motionPreference(true)
    const { container } = render(<AstrorderLoader />)
    const group = container.querySelector('g')!
    expect(JSON.parse(group.getAttribute('data-animate')!).rotate).toBe(0)
    change(false)
    expect(container.querySelector('g')).toBe(group)
    expect(JSON.parse(group.getAttribute('data-animate')!).rotate).toBe(360)
    expect(group).toHaveAttribute('data-repeat', 'Infinity')
  })

  it('keeps approval and error visuals static under the current preference', () => {
    motionPreference(true)
    const { container, rerender } = render(<SessionActivityBorder status="waiting_approval" />)
    const approval = container.querySelector('rect')!
    expect(JSON.parse(approval.getAttribute('data-animate')!).opacity).toBe(0.7)
    expect(container.querySelector('svg')).toHaveStyle({ color: 'var(--astr-yellow)' })
    rerender(<SessionActivityBorder status="error" />)
    expect(JSON.parse(container.querySelector('rect')!.getAttribute('data-animate')!).opacity).toBe(0.65)
    expect(container.querySelector('svg')).toHaveStyle({ color: 'var(--astr-red)' })
  })

  it('removes the preference listener when the border unmounts', () => {
    const { listenerCount } = motionPreference(false)
    const { unmount } = render(<SessionActivityBorder status="running" />)
    expect(listenerCount()).toBe(1)
    unmount()
    expect(listenerCount()).toBe(0)
  })
})
