import { useEffect, useRef } from 'react'

export type EmbeddedCutout = {
  x: number
  y: number
  width: number
  height: number
  radius?: number
}

export type EmbeddedBounds = {
  x: number
  y: number
  width: number
  height: number
  cutouts?: EmbeddedCutout[]
}

export type EmbeddedDesktopBridge = {
  showEmbeddedPage?: (url: string, bounds: EmbeddedBounds, refresh: boolean) => Promise<void>
  hideEmbeddedPage?: () => Promise<void>
  showStatistics?: (url: string, bounds: EmbeddedBounds, refresh: boolean) => Promise<void>
  hideStatistics?: () => Promise<void>
  openExternal?: (address: string) => Promise<void>
}

export function resolveNativeEmbeddedBridge(desktop?: EmbeddedDesktopBridge | null) {
  if (!desktop) return null
  const show = desktop.showEmbeddedPage || desktop.showStatistics
  const hide = desktop.hideEmbeddedPage || desktop.hideStatistics
  if (!show || !hide) return null
  return { show, hide }
}

const FLOATING_OVERLAY_SELECTOR = [
  '.mantine-Tooltip-tooltip',
  '.mantine-Popover-dropdown',
  '.mantine-Menu-dropdown',
  '.mantine-Notification-root',
  '.attention-island-pill',
  '.attention-island-tray',
  '.confirm-popover-card',
  '.background-tasks-panel',
  '.background-tasks-trigger',
  '[data-native-overlay="true"]',
].join(', ')

const FLOATING_DIALOG_CLASSES = [
  'mantine-Popover-dropdown',
  'attention-island-tray',
  'confirm-popover-card',
  'background-tasks-panel',
]

function isElementVisible(el: HTMLElement): boolean {
  if (el.getAttribute('aria-hidden') === 'true') return false
  const style = getComputedStyle(el)
  if (style.display === 'none' || style.visibility === 'hidden') return false
  if (el.style.opacity === '0') return false
  if (el.style.opacity === '1') return true
  if (style.animationName && style.animationName !== 'none') return true
  const opacity = parseFloat(style.opacity || '1')
  if (Number.isFinite(opacity) && opacity < 0.02) return false
  return true
}

function isFloatingDialogElement(el: Element): boolean {
  if (!(el instanceof HTMLElement)) return false
  if (el.dataset.nativeOverlay === 'true') return true
  return FLOATING_DIALOG_CLASSES.some((cls) => el.classList.contains(cls))
}

function hasBlockingModalOverlay(): boolean {
  for (const el of document.querySelectorAll('.mantine-Modal-overlay, .mantine-Drawer-overlay')) {
    if (el instanceof HTMLElement && isElementVisible(el)) return true
  }
  for (const el of document.querySelectorAll('[role="dialog"], [role="alertdialog"]')) {
    if (!(el instanceof HTMLElement) || !isElementVisible(el)) continue
    if (el.getAttribute('role') === 'alertdialog') return true
    if (isFloatingDialogElement(el)) continue
    return true
  }
  return false
}

function collectOverlappingCutouts(hostRect: DOMRect): EmbeddedCutout[] {
  if (hostRect.width <= 0 || hostRect.height <= 0) return []
  const cutouts: EmbeddedCutout[] = []
  const seen = new Set<HTMLElement>()
  for (const node of document.querySelectorAll(FLOATING_OVERLAY_SELECTOR)) {
    if (!(node instanceof HTMLElement) || seen.has(node)) continue
    seen.add(node)
    if (!isElementVisible(node)) continue
    const rect = node.getBoundingClientRect()
    if (rect.width <= 0 || rect.height <= 0) continue
    const ix1 = Math.max(hostRect.left, rect.left)
    const iy1 = Math.max(hostRect.top, rect.top)
    const ix2 = Math.min(hostRect.right, rect.right)
    const iy2 = Math.min(hostRect.bottom, rect.bottom)
    if (ix2 <= ix1 || iy2 <= iy1) continue
    const style = getComputedStyle(node)
    const rawRadius = parseFloat(style.borderTopLeftRadius || '0')
    const radius = Number.isFinite(rawRadius) && rawRadius > 0
      ? Math.min(rawRadius, rect.width / 2, rect.height / 2)
      : 0
    cutouts.push({
      x: Math.floor(rect.left),
      y: Math.floor(rect.top),
      width: Math.ceil(rect.right) - Math.floor(rect.left),
      height: Math.ceil(rect.bottom) - Math.floor(rect.top),
      ...(radius > 0 ? { radius: Math.round(radius) } : {}),
    })
  }
  return cutouts
}

function boundsSignature(
  url: string,
  revision: number,
  hidden: boolean,
  bounds?: EmbeddedBounds,
): string {
  if (hidden) return `hidden:${url}:${revision}`
  if (!bounds) return ''
  const cuts = (bounds.cutouts || [])
    .map((c) => `${c.x},${c.y},${c.width},${c.height},${c.radius || 0}`)
    .join('|')
  return `${url}:${revision}:${Math.round(bounds.x)},${Math.round(bounds.y)},${Math.round(bounds.width)},${Math.round(bounds.height)}:${cuts}`
}

// A separate top-level WebView preserves first-party cookie semantics even
// though its native child window occupies the route's content area.
// Overlapping floating overlays (sidebar popovers/tooltips, Dynamic Island,
// toast notifications) punch crisp Win32 region cutouts instead of blanking
// the whole page; full-screen modals still hide the child view cleanly.
export function NativeEmbeddedView({
  url,
  title,
  revision,
  desktop,
  onError,
}: {
  url: string
  title: string
  revision: number
  desktop: { show: (url: string, bounds: EmbeddedBounds, refresh: boolean) => Promise<void>; hide: () => Promise<void> }
  onError: (message: string) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let active = true
    let first = true
    let lastSig = ''
    let rafId: number | null = null
    let settleTimers: number[] = []

    const clearSettleTimers = () => {
      for (const id of settleTimers) window.clearTimeout(id)
      settleTimers = []
    }

    const syncNow = () => {
      rafId = null
      if (!active || !ref.current) return
      if (hasBlockingModalOverlay()) {
        const sig = boundsSignature(url, revision, true)
        if (sig !== lastSig) {
          lastSig = sig
          void desktop.hide().catch((cause: unknown) => {
            if (active) onError(cause instanceof Error ? cause.message : '隐藏嵌入页面失败')
          })
        }
        return
      }
      const hostRect = ref.current.getBoundingClientRect()
      const cutouts = collectOverlappingCutouts(hostRect)
      const bounds: EmbeddedBounds = {
        x: hostRect.x,
        y: hostRect.y,
        width: hostRect.width,
        height: hostRect.height,
        ...(cutouts.length > 0 ? { cutouts } : {}),
      }
      const refresh = first && revision > 0
      const sig = boundsSignature(url, revision, false, bounds)
      if (!first && sig === lastSig) return
      first = false
      lastSig = sig
      void desktop.show(url, bounds, refresh).catch((cause: unknown) => {
        if (active) onError(cause instanceof Error ? cause.message : '打开嵌入页面失败')
      })
    }

    const scheduleSync = (withSettle = false) => {
      if (!active) return
      if (rafId === null) {
        rafId = window.requestAnimationFrame(syncNow)
      }
      if (withSettle) {
        clearSettleTimers()
        settleTimers.push(window.setTimeout(syncNow, 90))
        settleTimers.push(window.setTimeout(syncNow, 260))
      }
    }

    const resizeObserver = new ResizeObserver(() => scheduleSync(false))
    if (ref.current) resizeObserver.observe(ref.current)

    const observedOverlays = new Set<Element>()
    const observeDynamicOverlayContainers = () => {
      for (const el of document.querySelectorAll('.attention-island, .mantine-Notifications-root, .background-tasks')) {
        if (!observedOverlays.has(el)) {
          observedOverlays.add(el)
          resizeObserver.observe(el)
        }
      }
    }
    observeDynamicOverlayContainers()

    const mutations = new MutationObserver(() => {
      observeDynamicOverlayContainers()
      // Immediate sync handles synchronous modal toggles (and JSDOM tests) while rAF + settle tracks transitions.
      syncNow()
      scheduleSync(true)
    })
    mutations.observe(document.body, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ['role', 'style', 'class', 'aria-hidden', 'data-entered'],
    })

    const onWindowResize = () => scheduleSync(false)
    const onAnimEnd = () => scheduleSync(false)
    window.addEventListener('resize', onWindowResize)
    document.addEventListener('transitionend', onAnimEnd, true)
    document.addEventListener('animationend', onAnimEnd, true)

    syncNow()

    return () => {
      active = false
      if (rafId !== null) window.cancelAnimationFrame(rafId)
      clearSettleTimers()
      resizeObserver.disconnect()
      mutations.disconnect()
      window.removeEventListener('resize', onWindowResize)
      document.removeEventListener('transitionend', onAnimEnd, true)
      document.removeEventListener('animationend', onAnimEnd, true)
      void desktop.hide().catch((cause: unknown) => {
        console.error('关闭嵌入视图失败', cause instanceof Error ? cause.message : '未知错误')
      })
    }
  }, [url, revision, desktop, onError])
  return <div ref={ref} title={title} style={{ flex: 1, minHeight: 0, width: '100%' }} />
}
