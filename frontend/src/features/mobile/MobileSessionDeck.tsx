import { AnimatePresence, motion } from 'motion/react'
import { useState, type CSSProperties, type ReactNode } from 'react'
import { sessionCardPose, type SessionCardCut, type SessionCardPose } from './mobileGestures'

const tween = { duration: 0.48, ease: [0.2, 0.8, 0.2, 1] as const }
const rest = { x: 0, y: 0, rotateX: 0, rotateY: 0, rotateZ: 0, z: 0, scale: 1 }

export function MobileSessionDeck({ sessionKey, cut, drag, children }: { sessionKey: string; cut: SessionCardCut; drag: SessionCardPose | null; children: ReactNode }) {
  const [active, setActive] = useState(cut)
  if (cut !== active) setActive(cut)
  const leave = sessionCardPose(active, 'leave')
  const enter = sessionCardPose(active, 'enter')
  return (
    <div className="m-session-deck">
      <AnimatePresence initial={false}>
        <motion.div
          key={sessionKey}
          className={`m-session-stage is-${active}`}
          data-session-cut={active}
          initial={{ ...enter, opacity: 1 }}
          animate={drag ? { ...drag, opacity: 1 } : { ...rest, opacity: 1 }}
          exit={{ ...leave, opacity: 1 }}
          transition={drag ? { duration: 0 } : tween}
          style={{
            position: 'absolute',
            inset: 0,
            transformOrigin: drag?.origin || enter.origin,
            transformStyle: 'preserve-3d',
            WebkitTransformStyle: 'preserve-3d',
            backfaceVisibility: 'hidden',
            WebkitBackfaceVisibility: 'hidden',
            boxShadow: drag ? '0 28px 48px #0006' : '0 10px 28px #0002',
          } as CSSProperties}
        >
          {children}
        </motion.div>
      </AnimatePresence>
    </div>
  )
}

export type { SessionCardCut }
