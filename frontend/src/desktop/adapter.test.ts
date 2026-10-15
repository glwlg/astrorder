// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { isDesktopEnvironment, getBackendWsHost, getWsProtocol } from './adapter'

describe('desktop adapter', () => {
  const originalLocation = window.location

  beforeEach(() => {
    delete (window as unknown as { __ASTRORDER_BACKEND_HOST__?: string }).__ASTRORDER_BACKEND_HOST__
    delete (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      value: originalLocation,
      writable: true,
    })
  })

  it('detects desktop environment correctly', () => {
    expect(isDesktopEnvironment()).toBe(false)
    ;(window as unknown as { astrorderDesktop: unknown }).astrorderDesktop = {}
    expect(isDesktopEnvironment()).toBe(true)
  })

  it('returns window.location.host in standard browser environment', () => {
    Object.defineProperty(window, 'location', {
      value: { host: '127.0.0.1:30001', protocol: 'http:' },
      writable: true,
    })
    expect(getBackendWsHost()).toBe('127.0.0.1:30001')
    expect(getWsProtocol()).toBe('ws:')
  })

  it('returns injected backend host if __ASTRORDER_BACKEND_HOST__ is set', () => {
    Object.defineProperty(window, 'location', {
      value: { host: 'wails.localhost', protocol: 'http:' },
      writable: true,
    })
    window.__ASTRORDER_BACKEND_HOST__ = '127.0.0.1:30102'
    expect(getBackendWsHost()).toBe('127.0.0.1:30102')
  })

  it('never invents a backend port when the shell injected none', () => {
    Object.defineProperty(window, 'location', {
      value: { host: 'wails.localhost', protocol: 'http:' },
      writable: true,
    })
    expect(getBackendWsHost()).toBe('wails.localhost')
  })
})
