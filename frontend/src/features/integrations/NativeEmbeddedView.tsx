import { useEffect, useRef } from 'react'

export type EmbeddedBounds = { x: number; y: number; width: number; height: number }
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

// A separate top-level WebView preserves first-party cookie semantics even
// though its native child window occupies the route's content area.
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
    const update = () => {
      if (!active || !ref.current) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) {
        void desktop.hide().catch((cause: unknown) => {
          if (active) onError(cause instanceof Error ? cause.message : '隐藏嵌入页面失败')
        })
        return
      }
      const { x, y, width, height } = ref.current.getBoundingClientRect()
      const refresh = first && revision > 0
      first = false
      void desktop.show(url, { x, y, width, height }, refresh).catch((cause: unknown) => {
        if (active) onError(cause instanceof Error ? cause.message : '打开嵌入页面失败')
      })
    }
    const observer = new ResizeObserver(update)
    let dialogOpen = Boolean(document.querySelector('[role="dialog"], [role="alertdialog"]'))
    const dialogs = new MutationObserver(() => {
      const next = Boolean(document.querySelector('[role="dialog"], [role="alertdialog"]'))
      if (next !== dialogOpen) {
        dialogOpen = next
        update()
      }
    })
    dialogs.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ['role'] })
    if (ref.current) observer.observe(ref.current)
    window.addEventListener('resize', update)
    update()
    return () => {
      active = false
      observer.disconnect()
      dialogs.disconnect()
      window.removeEventListener('resize', update)
      void desktop.hide().catch((cause: unknown) => {
        console.error('关闭嵌入视图失败', cause instanceof Error ? cause.message : '未知错误')
      })
    }
  }, [url, revision, desktop, onError])
  return <div ref={ref} title={title} style={{ flex: 1, minHeight: 0, width: '100%' }} />
}
