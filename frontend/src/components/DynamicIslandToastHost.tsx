import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { ActionIcon, Loader } from '@mantine/core'
import { useReducedMotion } from '@mantine/hooks'
import { useNotifications, type NotificationData } from '@mantine/notifications'
import { IconAlertTriangle, IconCheck, IconInfoCircle, IconX } from '@tabler/icons-react'
import { AnimatePresence, motion } from 'motion/react'
import { useHasIslandNotificationHost } from '../domain/islandNotifications'
import {
  announceIslandMessage,
  dismissMantineToast,
  extractPlainText,
  isRichIslandToast,
  pruneSupersededTransientToasts,
  resolveIslandToastDurationMs,
  resolveIslandToastTone,
} from '../domain/islandToast'
import './attentionIsland.css'

const TOAST_RESUME_MIN_MS = 1000

const SPRING_TRANSITION = {
  type: 'spring' as const,
  stiffness: 420,
  damping: 26,
  mass: 0.82,
}

/**
 * Fallback Dynamic Island host for routes where AttentionIsland is not mounted
 * (standalone Settings window, Mobile workspace, Auth screens). When AttentionIsland
 * is active in the desktop shell, this host yields cleanly so notifications never duplicate.
 */
export function DynamicIslandToastHost() {
  const hasPrimaryHost = useHasIslandNotificationHost()
  const reducedMotion = useReducedMotion()
  const { notifications: items } = useNotifications()
  const activeToast: NotificationData | null = !hasPrimaryHost && items.length > 0 ? items[items.length - 1] : null
  const activeToastKey = activeToast
    ? `${activeToast.id || 'toast'}:${String(activeToast.title || '')}:${typeof activeToast.message === 'string' ? activeToast.message : 'node'}:${activeToast.loading ? '1' : '0'}`
    : ''

  const [paused, setPaused] = useState(false)
  const [liveAnnouncement, setLiveAnnouncement] = useState('')
  const [popSeq, setPopSeq] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)
  const timerRef = useRef<{ key: string; remainingMs: number; startedAt: number } | null>(null)
  const prevKeyRef = useRef<string>('')

  useEffect(() => {
    if (hasPrimaryHost) return
    pruneSupersededTransientToasts(items)
  }, [hasPrimaryHost, items])

  useEffect(() => {
    if (activeToastKey && activeToastKey !== prevKeyRef.current) {
      setPopSeq((n) => n + 1)
    }
    prevKeyRef.current = activeToastKey
  }, [activeToastKey])

  const durationMs = activeToast ? resolveIslandToastDurationMs(activeToast) : null
  const tone = activeToast ? resolveIslandToastTone(activeToast) : 'info'
  const rich = activeToast ? isRichIslandToast(activeToast) : false

  useEffect(() => {
    if (!activeToast) return
    const text = [extractPlainText(activeToast.title), extractPlainText(activeToast.message)]
      .filter(Boolean)
      .join('：')
    announceIslandMessage(text, tone === 'error' ? 'high' : 'normal', setLiveAnnouncement)
  }, [activeToast, activeToastKey, tone])

  useEffect(() => {
    if (!activeToast || durationMs === null) {
      timerRef.current = null
      return
    }
    let state = timerRef.current
    if (!state || state.key !== activeToastKey) {
      state = { key: activeToastKey, remainingMs: durationMs, startedAt: Date.now() }
      timerRef.current = state
    }
    if (paused) return
    state.startedAt = Date.now()
    const delay = Math.max(TOAST_RESUME_MIN_MS, state.remainingMs)
    const target = activeToast
    const timer = window.setTimeout(() => {
      if (timerRef.current?.key === activeToastKey) timerRef.current = null
      dismissMantineToast(target)
    }, delay)
    return () => {
      window.clearTimeout(timer)
      if (timerRef.current?.key === activeToastKey) {
        const elapsed = Date.now() - timerRef.current.startedAt
        timerRef.current.remainingMs = Math.max(TOAST_RESUME_MIN_MS, timerRef.current.remainingMs - elapsed)
      }
    }
  }, [activeToast, activeToastKey, durationMs, paused])

  if (hasPrimaryHost) return null

  const toneClass =
    tone === 'error'
      ? 'is-notice-error'
      : tone === 'warning'
        ? 'is-notice-attention'
        : tone === 'success'
          ? 'is-notice-success'
          : 'is-notice-info'

  return (
    <>
      <div className="attention-island-sr-live" aria-live={tone === 'error' ? 'assertive' : 'polite'} aria-atomic="true">
        {liveAnnouncement}
      </div>
      <AnimatePresence>
        {activeToast && (
          <div
            ref={rootRef}
            className={[
              'attention-island',
              'is-standalone',
              'is-notice',
              'is-toast',
              rich ? 'is-tall' : 'is-compact-toast',
              toneClass,
              paused ? 'is-paused' : '',
            ]
              .filter(Boolean)
              .join(' ')}
            role="region"
            aria-label="通知"
            onMouseEnter={() => setPaused(true)}
            onMouseLeave={() => setPaused(false)}
            onFocus={() => setPaused(true)}
            onBlur={(e) => {
              if (!rootRef.current?.contains(e.relatedTarget as Node | null)) setPaused(false)
            }}
          >
            <motion.div
              layout={!reducedMotion}
              initial={reducedMotion ? { opacity: 0 } : { opacity: 0, y: -14, scale: 0.78 }}
              animate={reducedMotion ? { opacity: 1 } : { opacity: 1, y: 0, scale: 1 }}
              exit={reducedMotion ? { opacity: 0 } : { opacity: 0, y: -12, scale: 0.82 }}
              transition={reducedMotion ? { duration: 0.14 } : SPRING_TRANSITION}
              className={[
                'attention-island-shell',
                rich ? 'is-tall' : 'is-compact-toast',
                toneClass,
                popSeq > 0 ? (popSeq % 2 === 0 ? 'is-pop-even' : 'is-pop-odd') : '',
              ]
                .filter(Boolean)
                .join(' ')}
              style={
                durationMs !== null
                  ? ({ '--island-notice-duration': `${durationMs}ms` } as CSSProperties)
                  : undefined
              }
            >
              <motion.div
                key={activeToastKey}
                initial={reducedMotion ? { opacity: 0 } : { opacity: 0, scale: 0.92, y: -4 }}
                animate={reducedMotion ? { opacity: 1 } : { opacity: 1, scale: 1, y: 0 }}
                transition={reducedMotion ? { duration: 0.12 } : SPRING_TRANSITION}
                className={`attention-island-toast-row${rich ? ' is-rich' : ''}`}
              >
                <span className={`attention-island-toast-emblem is-${tone}${rich ? ' is-lg' : ''}`} aria-hidden="true">
                  {tone === 'loading' ? (
                    <Loader size={rich ? 14 : 12} color="currentColor" />
                  ) : tone === 'error' || tone === 'warning' ? (
                    <IconAlertTriangle size={rich ? 16 : 13} stroke={2.2} />
                  ) : tone === 'success' ? (
                    <IconCheck size={rich ? 16 : 13} stroke={2.5} />
                  ) : (
                    <IconInfoCircle size={rich ? 16 : 13} stroke={2.1} />
                  )}
                </span>

                <div className="attention-island-toast-body">
                  {activeToast.title && (
                    <div className="attention-island-toast-title">{activeToast.title}</div>
                  )}
                  <div className={`attention-island-toast-message${activeToast.title ? ' has-title' : ''}`}>
                    {activeToast.message}
                  </div>
                </div>

                {items.length > 1 && (
                  <span className="attention-island-count" aria-label={`还有 ${items.length - 1} 条通知`}>
                    +{items.length - 1}
                  </span>
                )}

                {activeToast.withCloseButton !== false && (
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    color="gray"
                    radius="xl"
                    aria-label="关闭通知"
                    className="attention-island-close-btn"
                    onClick={() => dismissMantineToast(activeToast)}
                  >
                    <IconX size={13} />
                  </ActionIcon>
                )}
              </motion.div>

              {durationMs !== null && (
                <span
                  key={`progress:${activeToastKey}`}
                  className={`attention-island-progress ${toneClass}`}
                  aria-hidden="true"
                />
              )}
            </motion.div>
          </div>
        )}
      </AnimatePresence>
    </>
  )
}
