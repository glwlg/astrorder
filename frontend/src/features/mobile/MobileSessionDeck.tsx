import { AnimatePresence, motion } from 'motion/react'
import { useReducedMotion } from '@mantine/hooks'
import { useState, type CSSProperties, type ReactNode } from 'react'
import type { SessionCardCut, SessionCardPose } from './mobileGestures'

const tween = { duration: 0.24, ease: [0.22, 0.85, 0.3, 1] as const }

export function MobileSessionDeck({ sessionKey, cut, drag, children }: { sessionKey: string; cut: SessionCardCut; drag: SessionCardPose | null; children: ReactNode }) {
  const reducedMotion = useReducedMotion(undefined, { getInitialValueInEffect: false })
  const [active, setActive] = useState(cut)
  if (cut !== active) setActive(cut)
  const forward = active.startsWith('next')
  const enterOffset = reducedMotion ? 0 : forward ? 44 : -44
  const leaveOffset = reducedMotion ? 0 : forward ? -44 : 44
  const dragX = drag ? Math.max(-72, Math.min(72, drag.x * 0.35)) : 0
  return (
    <div className="m-session-deck" data-session-key={sessionKey}>
      <AnimatePresence initial={false}>
        <motion.div
          key={sessionKey}
          className={`m-session-stage is-${active}`}
          data-session-cut={active}
          initial={{ x: enterOffset, opacity: reducedMotion ? 1 : 0 }}
          animate={drag ? { x: dragX, opacity: 1 } : { x: 0, opacity: 1 }}
          exit={{ x: leaveOffset, opacity: 0 }}
          transition={drag || reducedMotion ? { duration: 0 } : tween}
          style={{
            position: 'absolute',
            inset: 0,
          } as CSSProperties}
        >
          {children}
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

export type { SessionCardCut }
