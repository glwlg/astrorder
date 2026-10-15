/**
 * 桌面与 Web 端集中适配层
 * 统一管理后端 Host、WebSocket 连接目标及桌面原生能力
 */

declare global {
  interface Window {
    __ASTRORDER_BACKEND_HOST__?: string
  }
}

export function isDesktopEnvironment(): boolean {
  if (typeof window === 'undefined') return false
  return Boolean(
    (window as unknown as { astrorderDesktop?: unknown }).astrorderDesktop ||
    window.__ASTRORDER_BACKEND_HOST__ ||
    window.location.host === 'wails.localhost'
  )
}

/**
 * WebView2 custom schemes cannot upgrade to WebSocket, so the Wails shell
 * injects the explicitly configured App origin. Without that injection there is
 * deliberately no default port: the shell must never pick a backend on its own.
 */
export function getBackendWsHost(): string {
  if (typeof window === 'undefined') return ''
  return window.__ASTRORDER_BACKEND_HOST__ || window.location.host
}

export function getWsProtocol(): string {
  if (typeof window === 'undefined') return 'ws:'
  return window.location.protocol === 'https:' ? 'wss:' : 'ws:'
}
