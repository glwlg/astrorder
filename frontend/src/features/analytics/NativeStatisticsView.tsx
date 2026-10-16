import { useEffect, useRef } from 'react'

export type StatisticsBounds = { x: number; y: number; width: number; height: number }
export type StatisticsDesktop = {
  showStatistics: (url: string, bounds: StatisticsBounds, refresh: boolean) => Promise<void>
  hideStatistics: () => Promise<void>
}

// A separate top-level WebView preserves first-party cookie semantics even
// though its native child window occupies the statistics route's content area.
export function NativeStatisticsView({ url, revision, desktop, onError }: {
  url: string; revision: number; desktop: StatisticsDesktop; onError: (message: string) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    let active = true
    let first = true
    const update = () => {
      if (!active || !ref.current) return
      if (document.querySelector('[role="dialog"], [role="alertdialog"]')) {
        void desktop.hideStatistics().catch((cause: unknown) => {
          if (active) onError(cause instanceof Error ? cause.message : '隐藏网关统计页失败')
        })
        return
      }
      const { x, y, width, height } = ref.current.getBoundingClientRect()
      const refresh = first && revision > 0
      first = false
      void desktop.showStatistics(url, { x, y, width, height }, refresh).catch((cause: unknown) => {
        if (active) onError(cause instanceof Error ? cause.message : '打开网关统计页失败')
      })
    }
    const observer = new ResizeObserver(update)
    let dialogOpen = Boolean(document.querySelector('[role="dialog"], [role="alertdialog"]'))
    const dialogs = new MutationObserver(() => {
      const next = Boolean(document.querySelector('[role="dialog"], [role="alertdialog"]'))
      if (next !== dialogOpen) { dialogOpen = next; update() }
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
      void desktop.hideStatistics().catch((cause: unknown) => {
        console.error('关闭网关统计视图失败', cause instanceof Error ? cause.message : '未知错误')
      })
    }
  }, [url, revision, desktop, onError])
  return <div ref={ref} title="网关统计页" style={{ flex: 1, minHeight: 0, width: '100%' }} />
}
