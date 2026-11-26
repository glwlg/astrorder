import { IconChevronRight } from '@tabler/icons-react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { AstrorderLoader } from './AnimatedStatus'

/** Closed means unmounted: large markdown/tool payloads are parsed only on demand. */
export function LazyDetails({
  className,
  summary,
  children,
  loading = false,
  defaultOpen = false,
  autoOpen,
  forceOpen = false,
}: {
  className?: string
  summary: ReactNode
  children: ReactNode
  loading?: boolean
  defaultOpen?: boolean
  autoOpen?: boolean
  forceOpen?: boolean
}) {
  const [open, setOpen] = useState(autoOpen ?? defaultOpen)
  const userOverrideRef = useRef<boolean | null>(null)
  const prevAutoOpenRef = useRef<boolean | undefined>(autoOpen)

  useEffect(() => {
    if (autoOpen === undefined || autoOpen === prevAutoOpenRef.current) return
    prevAutoOpenRef.current = autoOpen
    if (autoOpen) {
      if (userOverrideRef.current !== false) setOpen(true)
    } else if (userOverrideRef.current !== true) {
      setOpen(false)
    }
  }, [autoOpen])

  const isOpen = open || forceOpen

  const handleToggle = () => {
    setOpen((current) => {
      const next = !current
      userOverrideRef.current = next
      return next
    })
  }

  return <details className={['lazy-details', loading ? 'is-loading-active' : '', className].filter(Boolean).join(' ')} open={isOpen} onToggle={event => { if (event.target === event.currentTarget) setOpen(event.currentTarget.open) }}>
    <summary onClick={event => { event.preventDefault(); handleToggle() }}>
      <span className={`lazy-details-indicator${loading ? ' is-loading' : ''}`} aria-hidden="true">
        {loading ? <AstrorderLoader size={16} /> : <IconChevronRight size={14} />}
      </span>
      {summary}
    </summary>
    {isOpen && children}
  </details>
}
