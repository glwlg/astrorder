import { useReducedMotion } from '@mantine/hooks'
import { AnimatePresence, motion } from 'motion/react'
import type { SessionStatus } from '../domain/types'
import { SessionStarBorder } from './animations/SessionStarBorder'

export function AstrorderLoader({ size = 14 }: { size?: number }) {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" fill="none" aria-hidden="true">
      <motion.g
        initial={false}
        animate={{ rotate: reducedMotion ? 0 : 360 }}
        transition={{ duration: 2.4, repeat: reducedMotion ? 0 : Infinity, ease: 'linear' }}
        style={{ transformOrigin: '16px 16px' }}
      >
        <circle cx="16" cy="16" r="12" stroke="currentColor" strokeWidth="2" strokeDasharray="13 6" />
        <circle cx="16" cy="4" r="2" fill="currentColor" />
        <circle cx="28" cy="16" r="2" fill="currentColor" />
        <circle cx="16" cy="28" r="2" fill="currentColor" />
        <circle cx="4" cy="16" r="2" fill="currentColor" />
      </motion.g>
      <motion.path
        d="M16 8c1 5 3 7 8 8-5 1-7 3-8 8-1-5-3-7-8-8 5-1 7-3 8-8Z"
        fill="currentColor"
        animate={reducedMotion ? undefined : { scale: [0.85, 1.15, 0.85], opacity: [0.45, 1, 0.45] }}
        transition={{ duration: 1.5, repeat: Infinity, ease: 'easeInOut' }}
        style={{ transformOrigin: '16px 16px' }}
      />
    </svg>
  )
}

export function SessionActivityBorder({ status }: { status: SessionStatus }) {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  if (status === 'running') return <SessionStarBorder />
  if (status === 'idle') return null
  const color = status === 'waiting_approval'
    ? 'var(--astr-yellow)'
    : status === 'error'
      ? 'var(--astr-red)'
      : 'var(--session-running-color, var(--astr-indigo, #5b6cff))'
  return (
    <svg className="session-state-border" data-status={status} style={{ color }} aria-hidden="true">
      <AnimatePresence>
        {status === 'waiting_approval' && <motion.rect key="waiting" x="1.25" y="1.25" width="calc(100% - 2.5px)" height="calc(100% - 2.5px)" rx="7" fill="none" stroke="currentColor" strokeWidth="1.5" initial={{ opacity: 0 }} animate={{ opacity: reducedMotion ? 0.7 : [0.3, 0.85, 0.3] }} exit={{ opacity: 0 }} transition={{ duration: 1.8, repeat: reducedMotion ? 0 : Infinity, ease: 'easeInOut' }} />}
        {status === 'error' && <motion.rect key="error" x="1.25" y="1.25" width="calc(100% - 2.5px)" height="calc(100% - 2.5px)" rx="7" fill="none" stroke="currentColor" strokeWidth="1.5" initial={{ opacity: 0 }} animate={reducedMotion ? { opacity: 0.65 } : { opacity: [0, 1, 0.65], x: [0, -1.5, 1.5, -0.75, 0.75, 0] }} exit={{ opacity: 0 }} transition={{ duration: 0.45, ease: 'easeOut' }} />}
      </AnimatePresence>
    </svg>
  )
}
