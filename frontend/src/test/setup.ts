import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, vi } from 'vitest'

afterEach(cleanup)

// Node's native storage shadows JSDOM storage on newer runtimes.
// Keep browser tests isolated in their own window, without disk-backed state.
const browserWindow = (globalThis as typeof globalThis & { jsdom: { window: Window } }).jsdom.window
Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: browserWindow.localStorage })
Object.defineProperty(globalThis, 'sessionStorage', { configurable: true, value: browserWindow.sessionStorage })

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false, media: query, onchange: null,
    addListener: vi.fn(), removeListener: vi.fn(),
    addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn(),
  })),
})
class ResizeObserverMock {
  observe() {}
  unobserve() {}
  disconnect() {}
}
Object.defineProperty(globalThis, 'ResizeObserver', { configurable: true, writable: true, value: ResizeObserverMock })
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', { configurable: true, writable: true, value: vi.fn() })
Object.defineProperty(HTMLCanvasElement.prototype, 'getContext', { configurable: true, value: () => null })
// JSDOM lacks FontFaceSet; Mantine autosizing observes browser font loading.
Object.defineProperty(document, 'fonts', { configurable: true, value: new EventTarget() })
