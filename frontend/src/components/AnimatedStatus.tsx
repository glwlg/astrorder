import { AnimatePresence, motion, useReducedMotion } from 'motion/react'
import type { SessionStatus } from '../domain/types'

export function AstrorderLoader({ size = 14 }: { size?: number }) {
  const reducedMotion = useReducedMotion()
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
  const reducedMotion = useReducedMotion()
  const color = status === 'waiting_approval'
    ? 'var(--astr-yellow)'
    : status === 'error'
      ? 'var(--astr-red)'
      : 'var(--session-running-color, var(--astr-indigo, #5b6cff))'
  return (
    <svg className="session-running-arc" data-status={status} style={{ color }} aria-hidden="true">
      <AnimatePresence>
        {status === 'running' && <motion.g key="running" initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}>
          {[
            { dash: '0.18 0.82', offset: 0, width: 1 },
            { dash: '0.145 0.855', offset: -0.0175, width: 1.6 },
            { dash: '0.1 0.9', offset: -0.04, width: 2.2 },
          ].map(({ dash, offset, width }) => (
            <motion.rect
              key={dash}
              x="1.25"
              y="1.25"
              width="calc(100% - 2.5px)"
              height="calc(100% - 2.5px)"
              rx="7"
              fill="none"
              stroke="currentColor"
              strokeWidth={width}
              strokeLinecap="round"
              strokeOpacity="0.55"
              pathLength="1"
              strokeDasharray={dash}
              initial={{ strokeDashoffset: offset }}
              animate={{ strokeDashoffset: reducedMotion ? offset : [offset, offset - 1] }}
              transition={{ duration: 4.2, repeat: reducedMotion ? 0 : Infinity, ease: 'linear' }}
            />
          ))}
        </motion.g>}
        {status === 'waiting_approval' && <motion.rect key="waiting" x="1.25" y="1.25" width="calc(100% - 2.5px)" height="calc(100% - 2.5px)" rx="7" fill="none" stroke="currentColor" strokeWidth="1.5" initial={{ opacity: 0 }} animate={{ opacity: reducedMotion ? 0.7 : [0.3, 0.85, 0.3] }} exit={{ opacity: 0 }} transition={{ duration: 1.8, repeat: reducedMotion ? 0 : Infinity, ease: 'easeInOut' }} />}
        {status === 'error' && <motion.rect key="error" x="1.25" y="1.25" width="calc(100% - 2.5px)" height="calc(100% - 2.5px)" rx="7" fill="none" stroke="currentColor" strokeWidth="1.5" initial={{ opacity: 0 }} animate={reducedMotion ? { opacity: 0.65 } : { opacity: [0, 1, 0.65], x: [0, -1.5, 1.5, -0.75, 0.75, 0] }} exit={{ opacity: 0 }} transition={{ duration: 0.45, ease: 'easeOut' }} />}
      </AnimatePresence>
    </svg>
  )
}
