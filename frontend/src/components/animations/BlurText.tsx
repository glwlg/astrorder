import { motion, AnimatePresence } from 'motion/react'

export interface BlurTextProps {
  text: string
  className?: string
  duration?: number
  blur?: number
  delay?: number
}

export function BlurText({
  text,
  className = '',
  duration = 0.28,
  blur = 4,
  delay = 0,
}: BlurTextProps) {
  return (
    <AnimatePresence mode="wait" initial={false}>
      <motion.span
        key={text}
        className={className}
        initial={{ opacity: 0, filter: `blur(${blur}px)`, y: 3 }}
        animate={{ opacity: 1, filter: 'blur(0px)', y: 0 }}
        exit={{ opacity: 0, filter: `blur(${blur}px)`, y: -3 }}
        transition={{ duration, delay, ease: [0.16, 1, 0.3, 1] }}
        style={{ display: 'inline-block' }}
      >
        {text}
      </motion.span>
    </AnimatePresence>
  )
}
