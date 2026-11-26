import { isValidElement, type ReactNode } from 'react'
import { notifications, notificationsStore, type NotificationData } from '@mantine/notifications'

export type IslandToastTone = 'success' | 'error' | 'warning' | 'info' | 'loading'

const DEFAULT_TOAST_MS = 3200
const WARNING_TOAST_MS = 4400
const ERROR_TOAST_MS = 5600

export function resolveIslandToastTone(item: NotificationData): IslandToastTone {
  if (item.loading) return 'loading'
  const c = String(item.color || '').toLowerCase()
  if (c === 'red' || c === 'pink' || c === 'rose') return 'error'
  if (c === 'yellow' || c === 'orange' || c === 'amber') return 'warning'
  if (c === 'teal' || c === 'green' || c === 'emerald' || c === 'lime') return 'success'
  return 'info'
}

export function resolveIslandToastDurationMs(item: NotificationData): number | null {
  if (item.loading || item.autoClose === false) return null
  if (typeof item.autoClose === 'number' && item.autoClose > 0) return item.autoClose
  const tone = resolveIslandToastTone(item)
  if (tone === 'error') return ERROR_TOAST_MS
  if (tone === 'warning') return WARNING_TOAST_MS
  return DEFAULT_TOAST_MS
}

export function isRichIslandToast(item: NotificationData): boolean {
  if (item.title && item.message) return true
  if (isValidElement(item.message)) return true
  if (typeof item.message === 'string' && item.message.trim().length > 28) return true
  return false
}

export function extractPlainText(node: ReactNode): string {
  if (node === null || node === undefined || typeof node === 'boolean') return ''
  if (typeof node === 'string' || typeof node === 'number') return String(node)
  if (Array.isArray(node)) return node.map(extractPlainText).filter(Boolean).join(' ')
  if (isValidElement<{ children?: ReactNode }>(node)) {
    return extractPlainText(node.props?.children)
  }
  return ''
}

export function dismissMantineToast(item: NotificationData): void {
  if (item.id) {
    notifications.hide(item.id)
  } else {
    notifications.updateState(notificationsStore, (list) => list.filter((entry) => entry !== item))
  }
  item.onClose?.(item)
}

/**
 * Prunes older transient non-error toasts superseded by a newer toast so rapid
 * actions (e.g. toggling mode) morph cleanly in-place instead of replaying stale messages.
 */
export function pruneSupersededTransientToasts(items: readonly NotificationData[]): void {
  if (items.length <= 1) return
  const latest = items[items.length - 1]
  const hasSuperseded = items.slice(0, -1).some((item) => {
    const tone = resolveIslandToastTone(item)
    return !item.loading && item.autoClose !== false && tone !== 'error'
  })
  if (!hasSuperseded) return
  notifications.updateState(notificationsStore, (list) =>
    list.filter((item) => {
      if (item === latest) return true
      const tone = resolveIslandToastTone(item)
      return Boolean(item.loading || item.autoClose === false || tone === 'error')
    }),
  )
}

export function announceIslandMessage(
  text: string,
  priority: 'normal' | 'high' = 'normal',
  fallbackSetter?: (msg: string) => void,
): void {
  const clean = text.replace(/\s+/g, ' ').trim()
  if (!clean) return
  const doc = typeof document !== 'undefined'
    ? (document as Document & { ariaNotify?: (msg: string, opts?: { priority?: 'normal' | 'high' }) => void })
    : null
  if (doc && typeof doc.ariaNotify === 'function') {
    try {
      doc.ariaNotify(clean, { priority })
      return
    } catch {
      // fall through to live region
    }
  }
  fallbackSetter?.(clean)
}
